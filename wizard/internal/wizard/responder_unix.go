//go:build !windows

package wizard

import (
	"context"
	"errors"
)

// These are no-op stubs so the responder dispatch in responder.go compiles
// on non-Windows hosts. The runtime.GOOS == "windows" branch never actually
// reaches them; they exist purely to satisfy the linker.

func isElevatedWindows() bool { return false }

func windowsServiceInstalled() bool { return false }

func windowsRunKeyInstalled() bool { return false }

func installWindowsService(exe, workdir string) error {
	return errors.New("Windows-only")
}

func uninstallWindowsService() error {
	return errors.New("Windows-only")
}

func statusWindowsService(_ context.Context) error {
	return errors.New("Windows-only")
}
