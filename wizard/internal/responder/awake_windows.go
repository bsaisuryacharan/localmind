//go:build windows

package responder

import (
	"log"
	"sync"
	"sync/atomic"
	"syscall"
)

// claimCount tracks the number of in-flight wake claims. The first
// claim (0 -> 1) sets the thread execution state to keep the system
// awake; the last release (1 -> 0) clears it. Concurrent claims
// compose: as long as any claim is outstanding, the lock stays held.
var (
	claimCount int64
	stateMu    sync.Mutex

	// SetThreadExecutionState lives in kernel32.dll. golang.org/x/sys/windows
	// does not export it; we load it via LazyDLL the same way
	// keepalive_windows.go does. Keeping the LazyProc as a package var means
	// kernel32 is loaded once at first use and reused across claims.
	kernel32SetExecState = syscall.NewLazyDLL("kernel32.dll").NewProc("SetThreadExecutionState")
)

// SetThreadExecutionState flag bits, taken from Microsoft Learn. Defined
// here as untyped constants instead of pulled from x/sys because that
// package does not export them.
const (
	esSystemRequired   = 0x00000001
	esAwaymodeRequired = 0x00000040
	esContinuous       = 0x80000000
)

// awakeFlags is the combination of execution-state flags we set on the
// first claim. esContinuous makes the state persist until we explicitly
// clear it; esSystemRequired prevents idle sleep; esAwaymodeRequired
// keeps the machine logically awake (running background work) even if
// it otherwise looks idle to the user.
const awakeFlags = esSystemRequired | esAwaymodeRequired | esContinuous

func setExec(flags uintptr) error {
	r1, _, err := kernel32SetExecState.Call(flags)
	if r1 == 0 {
		// On failure SetThreadExecutionState returns 0 and GetLastError is
		// available via the third return. err itself is non-nil for any
		// syscall error; we surface both.
		if err != nil {
			return err
		}
		return syscall.EINVAL
	}
	return nil
}

func claimAwake() func() {
	if atomic.AddInt64(&claimCount, 1) == 1 {
		stateMu.Lock()
		if err := setExec(awakeFlags); err != nil {
			log.Printf("responder/awake: SetThreadExecutionState(set) failed: %v", err)
		}
		stateMu.Unlock()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			if atomic.AddInt64(&claimCount, -1) == 0 {
				stateMu.Lock()
				if err := setExec(esContinuous); err != nil {
					log.Printf("responder/awake: SetThreadExecutionState(clear) failed: %v", err)
				}
				stateMu.Unlock()
			}
		})
	}
}
