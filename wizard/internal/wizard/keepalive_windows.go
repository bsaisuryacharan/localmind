//go:build windows

package wizard

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"syscall"
	"time"
)

// keepaliveCommand re-execs the localmind binary as a hidden background
// worker that holds the SetThreadExecutionState flag for the lifetime of
// its process. Windows has no caffeinate/systemd-inhibit equivalent —
// the standard approach is to call the kernel32 API in a long-lived
// process.
//
// We deliberately do NOT use exec.CommandContext: the child must outlive
// the parent invocation (`localmind keepalive on` returns immediately).
func keepaliveCommand(_ context.Context) (*exec.Cmd, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("locate self: %w", err)
	}
	cmd := exec.Command(exe, "keepalive", "_keepalive-worker")
	// Hide the console window so the user doesn't see a flicker.
	cmd.SysProcAttr = &syscall.SysProcAttr{
		HideWindow:    true,
		CreationFlags: 0x08000000, // CREATE_NO_WINDOW
	}
	return cmd, nil
}

func keepaliveMechanism() string {
	return "SetThreadExecutionState"
}

// SetThreadExecutionState flag bits, taken from Microsoft Learn.
const (
	esContinuous       = 0x80000000
	esSystemRequired   = 0x00000001
	esAwaymodeRequired = 0x00000040
	// esDisplayRequired would also keep the screen on; not what we want.
)

// keepaliveWorker is the hidden background process started by
// keepaliveCommand. It tells Windows to keep the system from sleeping
// for as long as the process is alive, then blocks forever (parent will
// kill it with `localmind keepalive off`).
func keepaliveWorker(ctx context.Context) error {
	kernel32 := syscall.NewLazyDLL("kernel32.dll")
	setExec := kernel32.NewProc("SetThreadExecutionState")
	if err := setExec.Find(); err != nil {
		return fmt.Errorf("SetThreadExecutionState not found: %w", err)
	}

	const flags = esContinuous | esSystemRequired | esAwaymodeRequired
	if r1, _, _ := setExec.Call(uintptr(flags)); r1 == 0 {
		return fmt.Errorf("SetThreadExecutionState returned 0 (call failed)")
	}
	defer setExec.Call(uintptr(esContinuous)) // restore default before exit

	// The flag is process-wide; once set, the system honors it until the
	// process exits or we clear it. We just need to keep the process alive.
	// A long ticker keeps the goroutine scheduled occasionally so the
	// runtime knows we're not deadlocked.
	t := time.NewTicker(60 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-t.C:
		}
	}
}
