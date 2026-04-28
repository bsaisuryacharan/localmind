package wizard

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

// Keepalive prevents the host from going to sleep while the localmind stack
// is running. It is the simplest answer to "I want to use localmind from my
// phone": never let the laptop sleep in the first place.
//
// Subcommands:
//
//	localmind keepalive on        start sleep prevention
//	localmind keepalive off       stop it
//	localmind keepalive status    print current state
//
// Mechanism per OS:
//
//	macOS    spawn `caffeinate -d` in the background
//	Linux    spawn `systemd-inhibit --what=sleep:idle:handle-lid-switch sleep infinity`
//	Windows  fork the localmind binary into `_keepalive-worker` mode, which
//	         calls SetThreadExecutionState in a loop (see keepalive_windows.go)
//
// The worker PID is recorded under .localmind/keepalive.pid so `off` and
// `status` can find it.
func Keepalive(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return keepaliveUsage()
	}
	switch args[0] {
	case "on":
		return keepaliveOn(ctx)
	case "off":
		return keepaliveOff(ctx)
	case "status":
		return keepaliveStatus(ctx)
	case "_keepalive-worker":
		// Hidden subcommand used on Windows to host the SetThreadExecutionState
		// loop in a background process. No-op stub on unix; see the windows
		// build for the real implementation.
		return keepaliveWorker(ctx)
	case "-h", "--help", "help":
		return keepaliveUsage()
	}
	return fmt.Errorf("unknown keepalive subcommand: %s", args[0])
}

func keepaliveUsage() error {
	return fmt.Errorf("usage: localmind keepalive {on | off | status}")
}

func keepaliveOn(ctx context.Context) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	pidPath := keepalivePIDPath(root)

	if pid, ok := readPID(pidPath); ok && processAlive(pid) {
		fmt.Printf("keepalive already running (pid %d)\n", pid)
		return nil
	}

	cmd, err := keepaliveCommand(ctx)
	if err != nil {
		return err
	}
	cmd.Stdout = nil
	cmd.Stderr = nil
	cmd.Stdin = nil
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("start keepalive: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(pidPath), 0o755); err != nil {
		return fmt.Errorf("mkdir: %w", err)
	}
	if err := os.WriteFile(pidPath, []byte(strconv.Itoa(cmd.Process.Pid)), 0o644); err != nil {
		return fmt.Errorf("write pid file: %w", err)
	}
	// Release the OS process handle so the child can outlive us.
	_ = cmd.Process.Release()
	fmt.Printf("keepalive: started (pid %d, mechanism=%s)\n", cmd.Process.Pid, keepaliveMechanism())
	return nil
}

func keepaliveOff(ctx context.Context) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	pidPath := keepalivePIDPath(root)
	pid, ok := readPID(pidPath)
	if !ok {
		fmt.Println("keepalive: not running (no pid file)")
		return nil
	}
	if !processAlive(pid) {
		fmt.Printf("keepalive: stale pid %d (process gone); cleaning up\n", pid)
		_ = os.Remove(pidPath)
		return nil
	}
	if err := killProcess(pid); err != nil {
		return fmt.Errorf("stop pid %d: %w", pid, err)
	}
	_ = os.Remove(pidPath)
	fmt.Printf("keepalive: stopped (pid %d)\n", pid)
	return nil
}

func keepaliveStatus(ctx context.Context) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}
	pidPath := keepalivePIDPath(root)
	pid, ok := readPID(pidPath)
	if !ok {
		fmt.Println("keepalive: off")
		return nil
	}
	if !processAlive(pid) {
		fmt.Printf("keepalive: off (stale pid file at %s; clean with `localmind keepalive off`)\n", pidPath)
		return nil
	}
	fmt.Printf("keepalive: on (pid %d, mechanism=%s)\n", pid, keepaliveMechanism())
	return nil
}

func keepalivePIDPath(root string) string {
	return filepath.Join(root, ".localmind", "keepalive.pid")
}

func readPID(path string) (int, bool) {
	body, err := os.ReadFile(path)
	if err != nil {
		return 0, false
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(body)))
	if err != nil || pid <= 0 {
		return 0, false
	}
	return pid, true
}

// processAlive reports whether the process is still running. On unix
// FindProcess always succeeds, so we send signal 0 to test. Windows
// FindProcess returns an error if the pid is gone.
func processAlive(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	if runtime.GOOS == "windows" {
		// FindProcess on Windows opens a handle; it being non-nil means alive.
		return p != nil
	}
	return p.Signal(syscall.Signal(0)) == nil
}

func killProcess(pid int) error {
	p, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return p.Kill()
}

// keepaliveCommand builds the OS-specific child process. Defined per-OS
// in keepalive_unix.go / keepalive_windows.go.

// keepaliveMechanism returns a human-readable label for `status` output.
// Defined per-OS.

// keepaliveWorker is the Windows background-loop entry point; a no-op on unix.
// Defined per-OS.
