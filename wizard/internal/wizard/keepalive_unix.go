//go:build !windows

package wizard

import (
	"context"
	"fmt"
	"os/exec"
	"runtime"
)

// keepaliveCommand returns a not-yet-started exec.Cmd that, when run,
// blocks forever telling the OS to stay awake. We Start() it and stash
// the pid; the user kills it via `localmind keepalive off`.
//
// We deliberately do NOT use exec.CommandContext: the child needs to
// outlive the parent (which is the short-lived `localmind keepalive on`
// invocation, not a daemon).
func keepaliveCommand(_ context.Context) (*exec.Cmd, error) {
	switch runtime.GOOS {
	case "darwin":
		// `caffeinate -d` keeps the system AND display awake. Drop -d if
		// only system-awake is needed; for an HTTP-server use case the
		// system-awake form is what we want.
		if _, err := exec.LookPath("caffeinate"); err != nil {
			return nil, fmt.Errorf("caffeinate not found (macOS built-in; system path issue?): %w", err)
		}
		return exec.Command("caffeinate", "-d"), nil

	case "linux":
		// systemd-inhibit holds inhibit locks for the lifetime of its
		// child process. `sleep infinity` is a no-op child that stays
		// alive until killed.
		if _, err := exec.LookPath("systemd-inhibit"); err != nil {
			return nil, fmt.Errorf("systemd-inhibit not found; install systemd or run keepalive manually: %w", err)
		}
		return exec.Command(
			"systemd-inhibit",
			"--what=sleep:idle:handle-lid-switch",
			"--who=localmind",
			"--why=Keep stack reachable",
			"sleep", "infinity"), nil
	}
	return nil, fmt.Errorf("keepalive not supported on %s", runtime.GOOS)
}

func keepaliveMechanism() string {
	switch runtime.GOOS {
	case "darwin":
		return "caffeinate"
	case "linux":
		return "systemd-inhibit"
	}
	return runtime.GOOS
}

// keepaliveWorker is unused on unix. The unix mechanisms are inhibitor
// processes, not in-process API calls.
func keepaliveWorker(ctx context.Context) error {
	<-ctx.Done()
	return nil
}
