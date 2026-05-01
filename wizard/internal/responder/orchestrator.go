package responder

// orchestrator.go contains the small helper used by the responder's /agent/*
// routes to talk to the Python orchestrator sidecar (default
// http://localhost:7950). The orchestrator owns multi-agent runs and
// exposes:
//
//	POST /run                — start a run
//	GET  /stream/<graph_id>  — SSE stream of ChatMessages
//	GET  /history/<graph_id> — JSON array of past ChatMessages
//	POST /confirm/<graph_id> — accept/reject the orchestrator's plan
//	POST /inject/<graph_id>  — post a mid-flight @user message
//	POST /cancel/<graph_id>  — abort a run
//
// The responder simply proxies these so that the WebUI / CLI / phone only
// ever speak to one well-known port (7900) and one auth token. The
// orchestrator itself binds to 127.0.0.1 only.

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OrchestratorClient is a tiny HTTP client around the orchestrator sidecar.
// One instance is created per Server and reused across all /agent/* routes.
// Stdlib only — we deliberately avoid pulling in any dependency just for
// this thin shim.
type OrchestratorClient struct {
	BaseURL string
	// httpc is used for the small JSON request/response routes. SSE goes
	// through stream which uses its own client with no overall timeout.
	httpc  *http.Client
	stream *http.Client
}

// NewOrchestratorClient builds a client. baseURL should NOT have a trailing
// slash; it is normalized just in case.
func NewOrchestratorClient(baseURL string) *OrchestratorClient {
	return &OrchestratorClient{
		BaseURL: strings.TrimRight(baseURL, "/"),
		httpc:   &http.Client{Timeout: 5 * time.Second},
		// No timeout on the stream client: SSE is supposed to stay open
		// for the lifetime of a run, which can comfortably exceed any
		// reasonable wall-clock cap.
		stream: &http.Client{Timeout: 0},
	}
}

// proxyJSON forwards method+path with the request's body intact, then copies
// the upstream response status + body back to w. Used for /run, /history,
// /confirm, /inject, /cancel — anything that's small and JSON-shaped.
func (c *OrchestratorClient) proxyJSON(w http.ResponseWriter, r *http.Request, method, path string) {
	url := c.BaseURL + path

	// Buffer the inbound body so we can hand a fresh reader to the
	// upstream request. The bodies here are tiny (<1KB) so the cost is
	// negligible and the code stays simpler than streaming.
	var body io.Reader
	if r.Body != nil {
		buf, err := io.ReadAll(r.Body)
		if err != nil {
			http.Error(w, fmt.Sprintf("read request body: %v", err), http.StatusBadRequest)
			return
		}
		body = bytes.NewReader(buf)
	}

	ctx, cancel := context.WithTimeout(r.Context(), 5*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		http.Error(w, fmt.Sprintf("orchestrator request: %v", err), http.StatusInternalServerError)
		return
	}
	// Forward Content-Type so JSON bodies round-trip correctly.
	if ct := r.Header.Get("Content-Type"); ct != "" {
		req.Header.Set("Content-Type", ct)
	} else if method == http.MethodPost {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpc.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("orchestrator unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	// Pass through Content-Type and status, then stream the body. Don't
	// blindly copy every header — Connection/Keep-Alive/etc. are
	// connection-scoped and shouldn't leak across the proxy boundary.
	if ct := resp.Header.Get("Content-Type"); ct != "" {
		w.Header().Set("Content-Type", ct)
	}
	w.WriteHeader(resp.StatusCode)
	_, _ = io.Copy(w, resp.Body)
}

// proxyStream forwards an SSE stream from the orchestrator to the client.
// We use a manual bufio loop rather than httputil.ReverseProxy so we can
// flush after every line — ReverseProxy buffers internally and SSE
// latency matters here. The headers (text/event-stream, no-cache,
// keep-alive) are set by the caller before invoking this.
func (c *OrchestratorClient) proxyStream(w http.ResponseWriter, r *http.Request, path string) {
	url := c.BaseURL + path

	req, err := http.NewRequestWithContext(r.Context(), http.MethodGet, url, nil)
	if err != nil {
		http.Error(w, fmt.Sprintf("orchestrator request: %v", err), http.StatusInternalServerError)
		return
	}
	req.Header.Set("Accept", "text/event-stream")

	resp, err := c.stream.Do(req)
	if err != nil {
		http.Error(w, fmt.Sprintf("orchestrator unreachable: %v", err), http.StatusBadGateway)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		// Surface the upstream status so callers can distinguish
		// "graph not found" (404) from "orchestrator down" (502).
		w.WriteHeader(resp.StatusCode)
		_, _ = io.Copy(w, resp.Body)
		return
	}

	flusher, _ := w.(http.Flusher)
	// Make sure the headers go out NOW so EventSource on the client side
	// sees the open. Some browsers won't fire onopen until the first
	// flush.
	if flusher != nil {
		flusher.Flush()
	}

	// SSE is line-oriented (events are separated by blank lines, fields
	// by single newlines). A bufio.Reader.ReadBytes('\n') loop is the
	// simplest correct approach: copy each line as soon as it arrives,
	// flush, repeat.
	br := bufio.NewReader(resp.Body)
	for {
		line, err := br.ReadBytes('\n')
		if len(line) > 0 {
			if _, werr := w.Write(line); werr != nil {
				return
			}
			if flusher != nil {
				flusher.Flush()
			}
		}
		if err != nil {
			// io.EOF on a healthy stream end, or any read error — either
			// way we're done.
			return
		}
	}
}

// PostRun forwards /agent/run -> orchestrator POST /run.
func (c *OrchestratorClient) PostRun(w http.ResponseWriter, r *http.Request) {
	c.proxyJSON(w, r, http.MethodPost, "/run")
}

// GetHistory forwards /agent/history/<id> -> orchestrator GET /history/<id>.
func (c *OrchestratorClient) GetHistory(w http.ResponseWriter, r *http.Request, id string) {
	c.proxyJSON(w, r, http.MethodGet, "/history/"+id)
}

// PostConfirm forwards /agent/confirm/<id> -> orchestrator POST /confirm/<id>.
func (c *OrchestratorClient) PostConfirm(w http.ResponseWriter, r *http.Request, id string) {
	c.proxyJSON(w, r, http.MethodPost, "/confirm/"+id)
}

// PostInject forwards /agent/inject/<id> -> orchestrator POST /inject/<id>.
func (c *OrchestratorClient) PostInject(w http.ResponseWriter, r *http.Request, id string) {
	c.proxyJSON(w, r, http.MethodPost, "/inject/"+id)
}

// PostCancel forwards /agent/cancel/<id> -> orchestrator POST /cancel/<id>.
func (c *OrchestratorClient) PostCancel(w http.ResponseWriter, r *http.Request, id string) {
	c.proxyJSON(w, r, http.MethodPost, "/cancel/"+id)
}

// StreamGraph forwards /agent/stream/<id> -> orchestrator SSE /stream/<id>.
func (c *OrchestratorClient) StreamGraph(w http.ResponseWriter, r *http.Request, id string) {
	c.proxyStream(w, r, "/stream/"+id)
}
