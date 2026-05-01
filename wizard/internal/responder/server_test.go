package responder

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// newTestServer constructs a *Server with sane defaults for a unit test.
// The mux is built by New(), so handlers can be invoked directly via
// httptest.NewRecorder without standing up a real HTTP listener.
func newTestServer(t *testing.T, cfg Config) *Server {
	t.Helper()
	if cfg.WakeRunner == nil {
		cfg.WakeRunner = func(ctx context.Context) error { return nil }
	}
	return New(cfg)
}

// decodeJSON unmarshals a recorder body into a map for assertion.
func decodeJSON(t *testing.T, rr *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var out map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode json: %v body=%q", err, rr.Body.String())
	}
	return out
}

func TestHealthz_NoAuth(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, Config{WebUIURL: "http://127.0.0.1:1"})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	s.mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}
	if rr.Body.String() != "ok" {
		t.Fatalf("body=%q want ok", rr.Body.String())
	}
}

func TestStatus_StackUp(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	s := newTestServer(t, Config{WebUIURL: upstream.URL})
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/status", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}
	body := decodeJSON(t, rr)
	if got, _ := body["stack_running"].(bool); !got {
		t.Fatalf("stack_running=%v want true", body["stack_running"])
	}
	if got, _ := body["webui_url"].(string); got != upstream.URL {
		t.Fatalf("webui_url=%q want %q", got, upstream.URL)
	}
}

func TestStatus_StackDown(t *testing.T) {
	t.Parallel()
	// Spin up a server then immediately close it. Its URL now points at
	// a port that should refuse connections (modulo OS reuse races).
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	s := newTestServer(t, Config{WebUIURL: deadURL})
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/status", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}
	body := decodeJSON(t, rr)
	if got, _ := body["stack_running"].(bool); got {
		t.Fatalf("stack_running=true; expected false for closed upstream")
	}
}

func TestWake_StackAlreadyUp_ReturnsImmediately(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	var wakeCalls int32
	s := newTestServer(t, Config{
		WebUIURL: upstream.URL,
		WakeRunner: func(ctx context.Context) error {
			atomic.AddInt32(&wakeCalls, 1)
			return nil
		},
	})

	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/wake", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200", rr.Code)
	}
	body := decodeJSON(t, rr)
	if got, _ := body["already_running"].(bool); !got {
		t.Fatalf("already_running=%v want true", body["already_running"])
	}
	if got := atomic.LoadInt32(&wakeCalls); got != 0 {
		t.Fatalf("WakeRunner was called %d times; expected 0", got)
	}
}

func TestWake_RunsWakeRunnerWhenDown(t *testing.T) {
	t.Parallel()
	// The upstream is "alive" only after a flag flips. The flag flips
	// when WakeRunner runs. probeStack uses a 3s timeout and the wake
	// loop polls every 2 seconds, so we use a short WakeTimeout that
	// still allows for at least one poll cycle.
	var awake int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if atomic.LoadInt32(&awake) == 1 {
			w.WriteHeader(http.StatusOK)
			return
		}
		// Force the response to look "down" by hijacking + closing without
		// writing. Easier: return 500, but probeStack treats 500 as down
		// (it accepts 200-499 as "alive but maybe auth-walled").
		w.WriteHeader(http.StatusInternalServerError)
	}))
	t.Cleanup(upstream.Close)

	s := newTestServer(t, Config{
		WebUIURL:    upstream.URL,
		WakeTimeout: 10 * time.Second,
		WakeRunner: func(ctx context.Context) error {
			atomic.StoreInt32(&awake, 1)
			return nil
		},
	})

	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/wake", nil))

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d body=%s want 200", rr.Code, rr.Body.String())
	}
	body := decodeJSON(t, rr)
	if got, _ := body["woke"].(bool); !got {
		t.Fatalf("woke=%v want true; body=%s", body["woke"], rr.Body.String())
	}
}

func TestWake_PropagatesWakeRunnerError(t *testing.T) {
	t.Parallel()
	// Closed-then-dead upstream so probeStack returns false up front.
	dead := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	deadURL := dead.URL
	dead.Close()

	s := newTestServer(t, Config{
		WebUIURL:    deadURL,
		WakeTimeout: 5 * time.Second,
		WakeRunner: func(ctx context.Context) error {
			return errors.New("boom")
		},
	})
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodPost, "/wake", nil))

	if rr.Code != http.StatusGatewayTimeout {
		t.Fatalf("status=%d want 504", rr.Code)
	}
	body := decodeJSON(t, rr)
	if got, _ := body["woke"].(bool); got {
		t.Fatalf("woke=true on error path")
	}
	errMsg, _ := body["error"].(string)
	if errMsg == "" {
		t.Fatalf("expected non-empty error in body")
	}
	// The handler wraps the error as "wake: <inner>"; just check the
	// inner message survives the round-trip.
	if errMsg == "" || !contains(errMsg, "boom") {
		t.Fatalf("error=%q does not mention WakeRunner failure", errMsg)
	}
}

func TestWake_RejectsGET(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, Config{WebUIURL: "http://127.0.0.1:1"})
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/wake", nil))
	if rr.Code != http.StatusMethodNotAllowed {
		t.Fatalf("status=%d want 405", rr.Code)
	}
}

func TestSingleFlight_Coalesces(t *testing.T) {
	t.Parallel()
	sf := &singleFlight{}
	var calls int32
	const goroutines = 5

	var wg sync.WaitGroup
	wg.Add(goroutines)
	start := make(chan struct{})
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			<-start
			_ = sf.Do(func() error {
				atomic.AddInt32(&calls, 1)
				time.Sleep(100 * time.Millisecond)
				return nil
			})
		}()
	}
	close(start)
	wg.Wait()

	if got := atomic.LoadInt32(&calls); got != 1 {
		t.Fatalf("singleFlight called fn %d times; want exactly 1", got)
	}
}

func TestSingleFlight_PropagatesError(t *testing.T) {
	t.Parallel()
	sf := &singleFlight{}
	want := errors.New("inner failure")
	var wg sync.WaitGroup
	wg.Add(3)
	start := make(chan struct{})
	errs := make(chan error, 3)
	for i := 0; i < 3; i++ {
		go func() {
			defer wg.Done()
			<-start
			errs <- sf.Do(func() error {
				time.Sleep(50 * time.Millisecond)
				return want
			})
		}()
	}
	close(start)
	wg.Wait()
	close(errs)
	for err := range errs {
		if !errors.Is(err, want) {
			t.Fatalf("singleFlight err=%v want %v", err, want)
		}
	}
}

// contains is a tiny stdlib-only helper so we don't pull in strings just
// for one check inside an error-message assertion.
func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}

// ----- /agent/* tests -----

func TestAgentIndex_Serves200(t *testing.T) {
	t.Parallel()
	s := newTestServer(t, Config{WebUIURL: "http://127.0.0.1:1"})
	rr := httptest.NewRecorder()
	s.mux.ServeHTTP(rr, httptest.NewRequest(http.MethodGet, "/agent", nil))
	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/html") {
		t.Fatalf("Content-Type=%q want text/html prefix", ct)
	}
	if rr.Body.Len() == 0 {
		t.Fatalf("body is empty; expected embedded agent.html")
	}
}

func TestAgentRun_ProxiesUpstream(t *testing.T) {
	t.Parallel()

	var gotPath, gotMethod, gotBody string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotMethod = r.Method
		b, _ := io.ReadAll(r.Body)
		gotBody = string(b)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w, `{"graph_id":"abc","mode":"plan"}`)
	}))
	t.Cleanup(upstream.Close)

	s := newTestServer(t, Config{
		WebUIURL:        "http://127.0.0.1:1",
		OrchestratorURL: upstream.URL,
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/agent/run",
		strings.NewReader(`{"query":"hello"}`))
	req.Header.Set("Content-Type", "application/json")
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rr.Code, rr.Body.String())
	}
	if gotPath != "/run" {
		t.Fatalf("upstream path=%q want /run", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Fatalf("upstream method=%q want POST", gotMethod)
	}
	if gotBody != `{"query":"hello"}` {
		t.Fatalf("upstream body=%q want %q", gotBody, `{"query":"hello"}`)
	}
	body := decodeJSON(t, rr)
	if body["graph_id"] != "abc" {
		t.Fatalf("response graph_id=%v want abc", body["graph_id"])
	}
	if body["mode"] != "plan" {
		t.Fatalf("response mode=%v want plan", body["mode"])
	}
}

func TestAgentStream_PropagatesSSE(t *testing.T) {
	t.Parallel()

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/stream/") {
			t.Errorf("upstream got unexpected path %q", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Two SSE events, each 'data: <json>\n\n', flushed independently
		// so the proxy has something to copy line-by-line.
		fl, _ := w.(http.Flusher)
		_, _ = fmt.Fprint(w, "data: {\"seq\":1,\"speaker\":\"@orchestrator\",\"body\":\"hi\"}\n\n")
		if fl != nil {
			fl.Flush()
		}
		_, _ = fmt.Fprint(w, "data: {\"seq\":2,\"speaker\":\"@synthesizer\",\"body\":\"done\"}\n\n")
		if fl != nil {
			fl.Flush()
		}
	}))
	t.Cleanup(upstream.Close)

	s := newTestServer(t, Config{
		WebUIURL:        "http://127.0.0.1:1",
		OrchestratorURL: upstream.URL,
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/agent/stream/xyz", nil)
	s.mux.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status=%d want 200; body=%s", rr.Code, rr.Body.String())
	}
	ct := rr.Header().Get("Content-Type")
	if !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type=%q want text/event-stream prefix", ct)
	}
	got := rr.Body.String()
	if !strings.Contains(got, `"seq":1`) || !strings.Contains(got, `"seq":2`) {
		t.Fatalf("missing one or both events in proxied stream; got:\n%s", got)
	}
	if !strings.Contains(got, "@orchestrator") || !strings.Contains(got, "@synthesizer") {
		t.Fatalf("missing speaker tags in proxied stream; got:\n%s", got)
	}
}
