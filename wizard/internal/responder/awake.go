package responder

import "net/http"

// ClaimAwakeWindow tells the OS to keep the system awake until the
// returned release function is called. Implementations are
// platform-specific; see awake_<os>.go.
//
// Multiple concurrent claims compose: as long as any release has not
// yet been called, the wake lock stays held. Implementations must be
// safe for concurrent calls.
func ClaimAwakeWindow() (release func()) { return claimAwake() }

// KeepAwakeMiddleware wraps next so that for the duration of every
// request, the OS is held awake. Cheap to apply; safe to compose
// with other middleware.
//
// Intended to sit INSIDE requireToken so unauthenticated requests are
// rejected before we bother claiming a wake window: a 401-bound
// request shouldn't keep the laptop from sleeping.
func KeepAwakeMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		release := ClaimAwakeWindow()
		defer release()
		next.ServeHTTP(w, r)
	})
}
