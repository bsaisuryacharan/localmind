//go:build !windows

package responder

import (
	"log"
	"os/exec"
	"runtime"
	"sync"
)

// On unix-like systems we hold the OS awake by spawning a long-lived
// inhibitor child process. macOS uses `caffeinate -d` (display+system
// sleep prevented while the child lives); Linux uses
// `systemd-inhibit --what=sleep`. The first claim spawns the child;
// subsequent claims just bump the counter; the last release kills it.
var (
	awakeMu        sync.Mutex
	awakeCount     int
	awakeProc      *exec.Cmd
	awakeWarnOnce  sync.Once
	awakeUnsupOnce sync.Once
)

// inhibitorCmd returns the command to spawn for the current OS, or
// nil if no supported inhibitor is on PATH. The child is expected to
// stay alive until killed.
func inhibitorCmd() *exec.Cmd {
	switch runtime.GOOS {
	case "darwin":
		if path, err := exec.LookPath("caffeinate"); err == nil {
			// -d also blocks display sleep; -i would be system-only.
			// We use -d because phone-driven requests often involve
			// the user looking at the laptop screen anyway.
			return exec.Command(path, "-d")
		}
	case "linux":
		if path, err := exec.LookPath("systemd-inhibit"); err == nil {
			return exec.Command(path,
				"--what=sleep",
				"--who=localmind",
				"--why=serving request",
				"sleep", "1d",
			)
		}
	}
	return nil
}

func claimAwake() func() {
	awakeMu.Lock()
	awakeCount++
	if awakeCount == 1 {
		cmd := inhibitorCmd()
		if cmd == nil {
			awakeUnsupOnce.Do(func() {
				log.Printf("responder/awake: no caffeinate or systemd-inhibit on PATH; wake-lock is a no-op on %s", runtime.GOOS)
			})
		} else if err := cmd.Start(); err != nil {
			awakeWarnOnce.Do(func() {
				log.Printf("responder/awake: failed to start inhibitor %q: %v", cmd.Path, err)
			})
		} else {
			awakeProc = cmd
		}
	}
	awakeMu.Unlock()

	var once sync.Once
	return func() {
		once.Do(func() {
			awakeMu.Lock()
			awakeCount--
			if awakeCount <= 0 {
				awakeCount = 0
				proc := awakeProc
				awakeProc = nil
				awakeMu.Unlock()
				if proc != nil && proc.Process != nil {
					// Killing and reaping can briefly block; do it
					// off the request goroutine so release() never
					// stalls a handler's defer.
					go func() {
						_ = proc.Process.Kill()
						_, _ = proc.Process.Wait()
					}()
				}
				return
			}
			awakeMu.Unlock()
		})
	}
}
