// Package responder is a small HTTP service that runs on the host (not in
// docker) and lets phones / external clients ping the localmind stack
// without depending on the stack itself being up.
//
// It exists to support the "AI from my phone" use case: the user's phone
// has a stable URL via Tailscale Funnel that always works, even when
// docker compose isn't running. Endpoints:
//
//	GET  /healthz   liveness — always 200 if the responder is up
//	GET  /status    JSON: stack reachable? what's the WebUI URL?
//	POST /wake      brings the stack up if down; blocks until reachable
//
// The responder is run as a foreground process by `localmind responder run`.
// It is normally launched at user login by an OS-specific service unit
// installed via `localmind responder install`.
package responder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"
)

// Config tunes the responder.
type Config struct {
	Addr           string        // listen address; default ":7900"
	WebUIURL       string        // upstream WebUI to probe; default "http://localhost:3000"
	WakeTimeout    time.Duration // max time to spend waking the stack; default 60s
	WakeRunner     WakeRunner    // injected so tests don't fork docker
}

// WakeRunner brings the docker stack up. The default implementation shells
// out to `localmind up --no-profile` from the responder package's caller
// (we don't want a circular import between responder and wizard).
type WakeRunner func(ctx context.Context) error

// Server bundles the HTTP server and its dependencies. Use Run for the
// blocking entrypoint.
type Server struct {
	cfg    Config
	mux    *http.ServeMux
	wake   *singleFlight // ensures only one /wake at a time
	probe  *http.Client
}

// New constructs a Server. The caller MUST set cfg.WakeRunner.
func New(cfg Config) *Server {
	if cfg.Addr == "" {
		cfg.Addr = ":7900"
	}
	if cfg.WebUIURL == "" {
		cfg.WebUIURL = "http://localhost:3000"
	}
	if cfg.WakeTimeout == 0 {
		cfg.WakeTimeout = 60 * time.Second
	}
	cfg.WebUIURL = strings.TrimRight(cfg.WebUIURL, "/")

	s := &Server{
		cfg:   cfg,
		mux:   http.NewServeMux(),
		wake:  &singleFlight{},
		probe: &http.Client{Timeout: 3 * time.Second},
	}
	s.mux.HandleFunc("/healthz", s.handleHealthz)
	s.mux.HandleFunc("/status", s.handleStatus)
	s.mux.HandleFunc("/wake", s.handleWake)
	return s
}

// Run blocks until ctx is canceled or the server fails.
func (s *Server) Run(ctx context.Context) error {
	if s.cfg.WakeRunner == nil {
		return errors.New("responder: WakeRunner is required")
	}
	srv := &http.Server{
		Addr:              s.cfg.Addr,
		Handler:           s.mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	go func() {
		<-ctx.Done()
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_ = srv.Shutdown(shutdownCtx)
	}()

	log.Printf("responder: listening on %s, upstream=%s", s.cfg.Addr, s.cfg.WebUIURL)
	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func (s *Server) handleHealthz(w http.ResponseWriter, _ *http.Request) {
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) handleStatus(w http.ResponseWriter, r *http.Request) {
	stackUp := s.probeStack(r.Context())
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]any{
		"stack_running": stackUp,
		"webui_url":     s.cfg.WebUIURL,
		"responder":     "ok",
	})
}

// handleWake is idempotent: concurrent callers coalesce onto a single wake
// attempt via singleFlight, and a request that finds the stack already up
// returns immediately.
func (s *Server) handleWake(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "POST only", http.StatusMethodNotAllowed)
		return
	}

	if s.probeStack(r.Context()) {
		writeJSON(w, http.StatusOK, map[string]any{
			"already_running": true,
			"webui_url":       s.cfg.WebUIURL,
		})
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.WakeTimeout)
	defer cancel()

	err := s.wake.Do(func() error {
		log.Printf("responder: /wake — bringing stack up")
		if err := s.cfg.WakeRunner(ctx); err != nil {
			return fmt.Errorf("wake: %w", err)
		}
		// Poll until the WebUI is reachable or the budget runs out.
		t := time.NewTicker(2 * time.Second)
		defer t.Stop()
		for {
			if s.probeStack(ctx) {
				return nil
			}
			select {
			case <-ctx.Done():
				return fmt.Errorf("wake: timed out waiting for %s", s.cfg.WebUIURL)
			case <-t.C:
			}
		}
	})

	if err != nil {
		writeJSON(w, http.StatusGatewayTimeout, map[string]any{
			"woke":      false,
			"error":     err.Error(),
			"webui_url": s.cfg.WebUIURL,
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"woke":      true,
		"webui_url": s.cfg.WebUIURL,
	})
}

// probeStack returns true if the upstream WebUI accepts a GET on /.
// Uses a 3s timeout so it's safe to call from request handlers.
func (s *Server) probeStack(ctx context.Context) bool {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, s.cfg.WebUIURL+"/", nil)
	if err != nil {
		return false
	}
	resp, err := s.probe.Do(req)
	if err != nil {
		return false
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	// Open WebUI returns 200 on /; anything in 2xx-4xx counts as "the
	// stack is alive even if the page wants auth".
	return resp.StatusCode >= 200 && resp.StatusCode < 500
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// singleFlight coalesces concurrent /wake calls. The Go x/sync version
// keys by string; ours has only one key (the implicit "wake the stack")
// so a plain mutex + cached error is enough.
type singleFlight struct {
	mu     sync.Mutex
	doing  bool
	cond   *sync.Cond
	result error
}

func (sf *singleFlight) Do(fn func() error) error {
	sf.mu.Lock()
	if sf.cond == nil {
		sf.cond = sync.NewCond(&sf.mu)
	}
	if sf.doing {
		// Wait for the in-flight call to finish, then return its error.
		for sf.doing {
			sf.cond.Wait()
		}
		err := sf.result
		sf.mu.Unlock()
		return err
	}
	sf.doing = true
	sf.mu.Unlock()

	err := fn()

	sf.mu.Lock()
	sf.result = err
	sf.doing = false
	sf.cond.Broadcast()
	sf.mu.Unlock()
	return err
}
