//go:build windows

package responder

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// WakeDockerDesktop ensures Docker Desktop is running and reachable on
// Windows. After Modern Standby (S0ix) resume, Docker Desktop's vmnetd /
// WSL backend can be paused; `docker compose up` then fails immediately.
// We probe `docker info` first, and only spawn Docker Desktop if the
// daemon is unreachable.
//
// The caller is expected to provide a context with a deadline (typically
// 90s on Windows). We honor cancellation; we do not impose an internal
// budget.
func WakeDockerDesktop(ctx context.Context) error {
	// Fast path: docker is already reachable.
	if dockerInfoOK(ctx, 2*time.Second) {
		return nil
	}

	// Locate Docker Desktop.exe. Check the standard install locations in
	// order. We don't fail if we can't find it — maybe it's already
	// starting and just not yet reachable — but we log via the returned
	// error if the polling loop times out.
	candidates := []string{
		filepath.Join(os.Getenv("ProgramFiles"), "Docker", "Docker", "Docker Desktop.exe"),
		filepath.Join(os.Getenv("ProgramFiles(x86)"), "Docker", "Docker", "Docker Desktop.exe"),
		filepath.Join(os.Getenv("LOCALAPPDATA"), "Docker", "Docker Desktop.exe"),
	}
	var dockerDesktopPath string
	for _, c := range candidates {
		if c == "" {
			continue
		}
		if _, err := os.Stat(c); err == nil {
			dockerDesktopPath = c
			break
		}
	}

	if dockerDesktopPath != "" {
		// Spawn Docker Desktop and don't Wait — it runs as its own UI
		// process that lives well past our handler.
		spawn := exec.Command(dockerDesktopPath)
		if err := spawn.Start(); err != nil {
			return fmt.Errorf("spawn Docker Desktop (%s): %w", dockerDesktopPath, err)
		}
	}
	// If we didn't find a path, Docker Desktop may already be launching
	// (e.g. system resume). Fall through and poll anyway.

	// Poll docker info every 3 seconds until it succeeds or ctx expires.
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()
	for {
		if dockerInfoOK(ctx, 2*time.Second) {
			return nil
		}
		select {
		case <-ctx.Done():
			return fmt.Errorf("docker desktop did not become reachable within deadline: %w", ctx.Err())
		case <-ticker.C:
		}
	}
}

// dockerInfoOK runs `docker info` with a short per-attempt timeout and
// returns true on exit code 0. We discard stdout/stderr — we only care
// about the exit status.
func dockerInfoOK(parent context.Context, perAttempt time.Duration) bool {
	ctx, cancel := context.WithTimeout(parent, perAttempt)
	defer cancel()
	cmd := exec.CommandContext(ctx, "docker", "info")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	return cmd.Run() == nil
}
