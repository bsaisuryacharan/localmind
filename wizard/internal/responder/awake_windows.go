//go:build windows

package responder

import (
	"log"
	"sync"
	"sync/atomic"

	"golang.org/x/sys/windows"
)

// claimCount tracks the number of in-flight wake claims. The first
// claim (0 -> 1) sets the thread execution state to keep the system
// awake; the last release (1 -> 0) clears it. Concurrent claims
// compose: as long as any claim is outstanding, the lock stays held.
var (
	claimCount int64
	stateMu    sync.Mutex
)

// awakeFlags is the combination of execution-state flags we set on
// the first claim. ES_CONTINUOUS makes the state persist until we
// explicitly clear it; ES_SYSTEM_REQUIRED prevents idle sleep;
// ES_AWAYMODE_REQUIRED keeps the machine logically awake (running
// background work) even if it otherwise looks idle to the user.
const awakeFlags = windows.ES_SYSTEM_REQUIRED | windows.ES_AWAYMODE_REQUIRED | windows.ES_CONTINUOUS

func claimAwake() func() {
	if atomic.AddInt64(&claimCount, 1) == 1 {
		stateMu.Lock()
		if _, err := windows.SetThreadExecutionState(awakeFlags); err != nil {
			log.Printf("responder/awake: SetThreadExecutionState(set) failed: %v", err)
		}
		stateMu.Unlock()
	}

	var once sync.Once
	return func() {
		once.Do(func() {
			if atomic.AddInt64(&claimCount, -1) == 0 {
				stateMu.Lock()
				if _, err := windows.SetThreadExecutionState(windows.ES_CONTINUOUS); err != nil {
					log.Printf("responder/awake: SetThreadExecutionState(clear) failed: %v", err)
				}
				stateMu.Unlock()
			}
		})
	}
}
