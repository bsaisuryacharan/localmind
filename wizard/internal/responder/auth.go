package responder

import (
	"crypto/subtle"
	"net/http"
	"strings"
)

// requireToken wraps next so that, when token is non-empty, every request
// must present a matching bearer token. The token may be passed as either:
//
//	Authorization: Bearer <token>
//	?token=<token>
//
// The query-parameter form exists so that the embedded HTML page (which is
// loaded via plain GET, where browsers can't easily inject Authorization
// headers) can authenticate itself by appending ?token=... to the URL.
//
// Comparison is constant-time to avoid leaking the configured token via
// timing side channels.
func requireToken(token string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token == "" {
			next.ServeHTTP(w, r)
			return
		}
		got := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		if got == "" {
			got = r.URL.Query().Get("token")
		}
		if subtle.ConstantTimeCompare([]byte(got), []byte(token)) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}
