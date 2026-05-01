package wizard

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Agent is the entry point for `localmind agent ...`.
//
// Subcommands:
//
//	run "<query>"       Run a query against the orchestrator. Renders the
//	                    group-chat stream live as a TUI. Captures stdin for
//	                    mid-flight @user messages.
//	list                List recent runs (graph_id, mode, status, summary).
//	show <id>           Replay a past run's full chat history.
//	cancel <id>         Cancel an in-flight run.
//
// Configuration via env vars:
//
//	LOCALMIND_ORCHESTRATOR_URL   default http://localhost:7950
//	LOCALMIND_RESPONDER_TOKEN    forwarded if set (matches the responder's auth).
//
// The CLI talks directly to the orchestrator on 127.0.0.1; the responder
// proxy is only on the path for remote (phone / WebUI) callers. Local
// stdin -> /inject and y/n -> /confirm round-trips need the lowest possible
// latency, which is why we skip the proxy hop here.
func Agent(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return agentUsage()
	}
	switch args[0] {
	case "run":
		return agentRun(ctx, args[1:])
	case "list":
		return agentList(ctx, args[1:])
	case "show":
		return agentShow(ctx, args[1:])
	case "cancel":
		return agentCancel(ctx, args[1:])
	case "-h", "--help", "help":
		return agentUsage()
	}
	return fmt.Errorf("unknown agent subcommand: %s", args[0])
}

func agentUsage() error {
	return fmt.Errorf("usage: localmind agent {run \"<query>\" | list | show <id> | cancel <id>}")
}

// ChatMessage mirrors the Pydantic shape emitted by the orchestrator.
type ChatMessage struct {
	GraphID string                 `json:"graph_id"`
	Seq     int                    `json:"seq"`
	TsUnix  float64                `json:"ts_unix"`
	Speaker string                 `json:"speaker"`
	Body    string                 `json:"body"`
	Kind    string                 `json:"kind"`
	Refs    []string               `json:"refs"`
	Meta    map[string]interface{} `json:"meta"`
}

// orchestratorBase returns the orchestrator URL with no trailing slash.
func orchestratorBase() string {
	u := strings.TrimSpace(os.Getenv("LOCALMIND_ORCHESTRATOR_URL"))
	if u == "" {
		u = "http://localhost:7950"
	}
	return strings.TrimRight(u, "/")
}

// shortClient is for non-streaming JSON calls.
func shortClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

// streamClient has no timeout (SSE connection).
func streamClient() *http.Client {
	return &http.Client{Timeout: 0}
}

// withAuth applies the responder bearer token if one is configured. The
// token is shared with the responder so the orchestrator can use the same
// secret when it's reverse-proxied.
func withAuth(req *http.Request) {
	if tok := strings.TrimSpace(os.Getenv("LOCALMIND_RESPONDER_TOKEN")); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
}

// postJSON sends a JSON-encoded body and returns the response. Caller closes.
func postJSON(ctx context.Context, client *http.Client, url string, body interface{}) (*http.Response, error) {
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			return nil, fmt.Errorf("encode body: %w", err)
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, &buf)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	withAuth(req)
	return client.Do(req)
}

func getJSON(ctx context.Context, client *http.Client, url string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	withAuth(req)
	return client.Do(req)
}

// --- agent run --------------------------------------------------------------

type runResponse struct {
	GraphID string `json:"graph_id"`
	Mode    string `json:"mode"`
}

func agentRun(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: localmind agent run \"<query>\"")
	}
	query := strings.Join(args, " ")
	base := orchestratorBase()

	resp, err := postJSON(ctx, shortClient(), base+"/run", map[string]interface{}{"query": query})
	if err != nil {
		return fmt.Errorf("post /run: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("orchestrator /run %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var rr runResponse
	if err := json.NewDecoder(resp.Body).Decode(&rr); err != nil {
		return fmt.Errorf("decode /run: %w", err)
	}
	if rr.GraphID == "" {
		return errors.New("orchestrator returned empty graph_id")
	}

	w := newTermWriter()
	fmt.Printf("==> graph: %s (mode: %s)\n", rr.GraphID, rr.Mode)

	// Cancellable child context so stdin reader / SSE reader can stop one another.
	streamCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	// Stdin reader: forwards plain lines to /inject. confirm-request prompts
	// are handled inline by the SSE goroutine (synchronously, since we want
	// the user's reply to flow through /confirm with no race against the
	// next streamed message). We share this mutex so a confirm prompt in
	// progress doesn't compete with a free-form @user line.
	var promptMu sync.Mutex

	go stdinForwarder(streamCtx, base, rr.GraphID, &promptMu)

	if err := streamRun(streamCtx, base, rr.GraphID, w, &promptMu); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	return nil
}

// streamRun opens the SSE stream and renders each message. Returns nil on
// natural end-of-stream OR after a `final`/`error` terminator.
func streamRun(ctx context.Context, base, graphID string, w *termWriter, promptMu *sync.Mutex) error {
	url := fmt.Sprintf("%s/stream/%s", base, graphID)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	withAuth(req)

	resp, err := streamClient().Do(req)
	if err != nil {
		return fmt.Errorf("open SSE: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("orchestrator /stream %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}

	// SSE framing: lines beginning `data: <payload>`, blank line ends event.
	// The orchestrator sends one JSON ChatMessage per `data:` line.
	sc := bufio.NewScanner(resp.Body)
	// Allow large bodies (model output can be long).
	sc.Buffer(make([]byte, 64*1024), 1024*1024)

	var dataBuf strings.Builder
	flush := func() error {
		if dataBuf.Len() == 0 {
			return nil
		}
		payload := dataBuf.String()
		dataBuf.Reset()
		var msg ChatMessage
		if err := json.Unmarshal([]byte(payload), &msg); err != nil {
			// Tolerate non-JSON keepalives; just print dimmed.
			w.note(fmt.Sprintf("(unparsed event: %s)", strings.TrimSpace(payload)))
			return nil
		}
		w.renderChatMessage(msg)
		switch msg.Kind {
		case "confirm-request":
			handleConfirm(ctx, base, graphID, promptMu, w)
		case "final":
			if msg.Speaker == "@synthesizer" {
				return io.EOF
			}
		case "error":
			return io.EOF
		}
		return nil
	}

	for sc.Scan() {
		line := sc.Text()
		if line == "" {
			if err := flush(); err != nil {
				if errors.Is(err, io.EOF) {
					return nil
				}
				return err
			}
			continue
		}
		if strings.HasPrefix(line, ":") {
			// SSE comment / heartbeat
			continue
		}
		if strings.HasPrefix(line, "data:") {
			payload := strings.TrimPrefix(line, "data:")
			payload = strings.TrimPrefix(payload, " ")
			if dataBuf.Len() > 0 {
				dataBuf.WriteByte('\n')
			}
			dataBuf.WriteString(payload)
			continue
		}
		// other SSE fields (event:, id:, retry:) — ignore
	}
	if err := sc.Err(); err != nil && !errors.Is(err, context.Canceled) {
		return err
	}
	// Drain final event if present without a trailing blank line.
	_ = flush()
	return nil
}

// stdinForwarder reads lines from stdin and POSTs them as @user injections.
// Lines starting with `y`/`n`/`yes`/`no`/`edit` while no confirm prompt is
// pending are still forwarded as plain injections — the orchestrator can
// decide how to handle them. The promptMu lock blocks injection while a
// synchronous confirm prompt has the foreground.
func stdinForwarder(ctx context.Context, base, graphID string, promptMu *sync.Mutex) {
	r := bufio.NewReader(os.Stdin)
	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		line, err := r.ReadString('\n')
		line = strings.TrimRight(line, "\r\n")
		if line != "" {
			promptMu.Lock()
			postInject(ctx, base, graphID, line)
			promptMu.Unlock()
		}
		if err != nil {
			// stdin closed (pipe), or context cancellation surfaces here.
			return
		}
	}
}

func postInject(ctx context.Context, base, graphID, body string) {
	url := fmt.Sprintf("%s/inject/%s", base, graphID)
	resp, err := postJSON(ctx, shortClient(), url, map[string]interface{}{"body": body})
	if err != nil {
		fmt.Fprintf(os.Stderr, "inject: %v\n", err)
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		fmt.Fprintf(os.Stderr, "inject %s: %s\n", resp.Status, strings.TrimSpace(string(b)))
	}
}

// handleConfirm reads y/n/edit from stdin and POSTs to /confirm. It holds
// promptMu so the regular stdin forwarder doesn't see (and forward) the
// confirmation reply as if it were a normal injection. If stdin is not a
// TTY we do not wait — the user is presumed to be scripting and should drive
// the prompt with explicit `agent run` / SDK calls.
func handleConfirm(ctx context.Context, base, graphID string, promptMu *sync.Mutex, w *termWriter) {
	if !w.isTTY {
		w.note("(non-TTY: skipping confirm prompt; send {accepted:true|false} via /confirm directly)")
		return
	}
	promptMu.Lock()
	defer promptMu.Unlock()

	fmt.Print("Confirm? [y/n/edit]: ")
	r := bufio.NewReader(os.Stdin)
	line, _ := r.ReadString('\n')
	answer := strings.ToLower(strings.TrimSpace(line))

	url := fmt.Sprintf("%s/confirm/%s", base, graphID)
	switch answer {
	case "y", "yes":
		_ = postConfirm(ctx, url, map[string]interface{}{"accepted": true})
	case "n", "no":
		_ = postConfirm(ctx, url, map[string]interface{}{"accepted": false})
		// the orchestrator should emit a `final`/`error` message that ends
		// the stream; we just return here.
	case "edit":
		fmt.Println("Paste edited plan as JSON; finish with a blank line:")
		var sb strings.Builder
		for {
			ln, err := r.ReadString('\n')
			if strings.TrimSpace(ln) == "" {
				break
			}
			sb.WriteString(ln)
			if err != nil {
				break
			}
		}
		var edits interface{}
		if err := json.Unmarshal([]byte(sb.String()), &edits); err != nil {
			fmt.Fprintf(os.Stderr, "edit: invalid JSON (%v); treating as plain accept\n", err)
			_ = postConfirm(ctx, url, map[string]interface{}{"accepted": true})
			return
		}
		_ = postConfirm(ctx, url, map[string]interface{}{"accepted": true, "edits": edits})
	default:
		fmt.Fprintf(os.Stderr, "unrecognized answer %q; treating as 'no'\n", answer)
		_ = postConfirm(ctx, url, map[string]interface{}{"accepted": false})
	}
}

func postConfirm(ctx context.Context, url string, body map[string]interface{}) error {
	resp, err := postJSON(ctx, shortClient(), url, body)
	if err != nil {
		fmt.Fprintf(os.Stderr, "confirm: %v\n", err)
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		fmt.Fprintf(os.Stderr, "confirm %s: %s\n", resp.Status, strings.TrimSpace(string(b)))
	}
	return nil
}

// --- agent list -------------------------------------------------------------

func agentList(ctx context.Context, _ []string) error {
	base := orchestratorBase()
	resp, err := getJSON(ctx, shortClient(), base+"/list")
	if err != nil {
		// If the orchestrator is reachable but /list isn't implemented yet,
		// the request returns 404. A connection error means the daemon is
		// down — surface that as-is.
		fmt.Println("list endpoint not yet wired in v0.3.0; use `localmind agent show <id>`")
		return nil
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		fmt.Println("list endpoint not yet wired in v0.3.0; use `localmind agent show <id>`")
		return nil
	}
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("orchestrator /list %s: %s", resp.Status, strings.TrimSpace(string(b)))
	}
	// Print the JSON array as a small table. We don't know the exact shape
	// guaranteed by slot 1, so we tolerate arbitrary fields.
	var rows []map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&rows); err != nil {
		return fmt.Errorf("decode /list: %w", err)
	}
	if len(rows) == 0 {
		fmt.Println("(no runs)")
		return nil
	}
	for _, row := range rows {
		gid := strOf(row["graph_id"])
		mode := strOf(row["mode"])
		status := strOf(row["status"])
		summary := strOf(row["summary"])
		fmt.Printf("%s  mode=%-10s status=%-10s %s\n", gid, mode, status, summary)
	}
	return nil
}

func strOf(v interface{}) string {
	if v == nil {
		return ""
	}
	switch x := v.(type) {
	case string:
		return x
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(x)
	default:
		b, _ := json.Marshal(x)
		return string(b)
	}
}

// --- agent show -------------------------------------------------------------

func agentShow(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: localmind agent show <graph_id>")
	}
	id := args[0]
	base := orchestratorBase()
	resp, err := getJSON(ctx, shortClient(), fmt.Sprintf("%s/history/%s", base, id))
	if err != nil {
		return fmt.Errorf("get /history: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("orchestrator /history/%s %s: %s", id, resp.Status, strings.TrimSpace(string(b)))
	}
	var msgs []ChatMessage
	if err := json.NewDecoder(resp.Body).Decode(&msgs); err != nil {
		return fmt.Errorf("decode history: %w", err)
	}
	w := newTermWriter()
	for _, m := range msgs {
		w.renderChatMessage(m)
	}
	return nil
}

// --- agent cancel -----------------------------------------------------------

func agentCancel(ctx context.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: localmind agent cancel <graph_id>")
	}
	id := args[0]
	base := orchestratorBase()
	resp, err := postJSON(ctx, shortClient(), fmt.Sprintf("%s/cancel/%s", base, id), nil)
	if err != nil {
		return fmt.Errorf("post /cancel: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("orchestrator /cancel/%s %s: %s", id, resp.Status, strings.TrimSpace(string(b)))
	}
	fmt.Printf("cancelled: %s\n", id)
	return nil
}

// --- TUI --------------------------------------------------------------------

// termWriter holds rendering config (TTY detection, terminal width, ANSI on/off).
type termWriter struct {
	isTTY  bool
	width  int
	startT time.Time
	// startUnix is the t=0 reference for computing relative latencies. We
	// take the first message's ts_unix as the baseline; this gives readable
	// "(1.4s)" style timings without needing wall-clock alignment.
	startUnix float64
	startSet  bool
}

func newTermWriter() *termWriter {
	w := &termWriter{startT: time.Now()}
	if fi, err := os.Stdout.Stat(); err == nil && (fi.Mode()&os.ModeCharDevice) != 0 {
		w.isTTY = true
	}
	w.width = 100
	if cols := strings.TrimSpace(os.Getenv("COLUMNS")); cols != "" {
		if n, err := strconv.Atoi(cols); err == nil && n > 20 {
			w.width = n
		}
	}
	return w
}

const (
	ansiReset   = "\033[0m"
	ansiDim     = "\033[2m"
	ansiBold    = "\033[1m"
	ansiCyan    = "\033[36m"
	ansiGreen   = "\033[32m"
	ansiYellow  = "\033[33m"
	ansiMagenta = "\033[35m"
	ansiBlue    = "\033[34m"
)

// speakerStyle returns the (prefix, suffix) ANSI codes for a given speaker.
// When isTTY is false both halves are empty so the output is plain text.
func (w *termWriter) speakerStyle(speaker string) (string, string) {
	if !w.isTTY {
		return "", ""
	}
	switch {
	case speaker == "@user":
		return ansiCyan, ansiReset
	case speaker == "@orchestrator":
		return ansiGreen, ansiReset
	case speaker == "@synthesizer":
		return ansiBold + ansiGreen, ansiReset
	case strings.HasPrefix(speaker, "@researcher"):
		return ansiYellow, ansiReset
	case strings.HasPrefix(speaker, "@reviewer"):
		return ansiMagenta, ansiReset
	case strings.HasPrefix(speaker, "@coder"):
		return ansiBlue, ansiReset
	default:
		return "", ""
	}
}

func (w *termWriter) dim(s string) string {
	if !w.isTTY {
		return s
	}
	return ansiDim + s + ansiReset
}

func (w *termWriter) note(s string) {
	fmt.Println(w.dim(s))
}

// renderChatMessage prints a single chat message in Slack-style format:
//
//	@speaker        (1.4s)  body line one
//	                        body line two ...     🔧 tool / ↩
//
// The speaker column is fixed at 16 chars (truncated/padded). Body lines
// after the first are indented to align under the first body char so the
// transcript reads like a chat log.
func (w *termWriter) renderChatMessage(msg ChatMessage) {
	if !w.startSet && msg.TsUnix > 0 {
		w.startUnix = msg.TsUnix
		w.startSet = true
	}

	const speakerCol = 16
	speaker := msg.Speaker
	if speaker == "" {
		speaker = "@?"
	}
	disp := speaker
	if len(disp) > speakerCol {
		disp = disp[:speakerCol]
	}
	pad := strings.Repeat(" ", speakerCol-len(disp))
	pre, post := w.speakerStyle(speaker)

	// Latency relative to first message.
	lat := ""
	if w.startSet && msg.TsUnix > 0 {
		dt := msg.TsUnix - w.startUnix
		if dt < 0 {
			dt = 0
		}
		lat = w.dim(fmt.Sprintf("(%.1fs)", dt))
	}

	// Glyphs based on kind.
	suffix := ""
	switch msg.Kind {
	case "tool-call":
		toolName := ""
		if msg.Meta != nil {
			if v, ok := msg.Meta["tool"]; ok {
				toolName = strOf(v)
			}
		}
		if toolName != "" {
			suffix = "  \U0001F527 " + toolName
		} else {
			suffix = "  \U0001F527"
		}
	case "tool-result":
		suffix = "  ↩"
	}

	// Build the prefix used on the FIRST line: "@speaker      (1.4s)  ".
	// indentLen tracks how many cells the prefix occupies on screen so we
	// can wrap subsequent body lines underneath the body, not the speaker.
	first := pre + disp + post + pad
	indentLen := speakerCol
	if lat != "" {
		first += " " + lat
		// Visible chars: 1 space + "(x.xs)". Approximate length without ANSI.
		indentLen += 1 + visibleLen(lat)
	}
	first += "  "
	indentLen += 2

	indent := strings.Repeat(" ", indentLen)
	avail := w.width - indentLen
	if avail < 20 {
		avail = 20
	}

	body := msg.Body
	if body == "" && suffix != "" {
		body = ""
	}
	wrapped := wrapBody(body, avail)
	if len(wrapped) == 0 {
		wrapped = []string{""}
	}
	// Append glyph suffix to the last line.
	if suffix != "" {
		wrapped[len(wrapped)-1] = wrapped[len(wrapped)-1] + suffix
	}

	fmt.Println(first + wrapped[0])
	for _, ln := range wrapped[1:] {
		fmt.Println(indent + ln)
	}
}

// visibleLen returns the rune count of s with ANSI escape sequences stripped.
// Used so we know how wide the rendered prefix is when we wrap body text.
func visibleLen(s string) int {
	n := 0
	in := false
	for _, r := range s {
		if r == '\033' {
			in = true
			continue
		}
		if in {
			if r == 'm' {
				in = false
			}
			continue
		}
		n++
	}
	return n
}

// wrapBody splits s into lines that fit within `width` cells. It honors
// embedded \n as hard breaks, then word-wraps long lines on whitespace.
func wrapBody(s string, width int) []string {
	if width <= 0 {
		return []string{s}
	}
	var out []string
	for _, hard := range strings.Split(s, "\n") {
		if hard == "" {
			out = append(out, "")
			continue
		}
		words := strings.Fields(hard)
		// Preserve leading whitespace (e.g. for indented bullet lines).
		leading := ""
		for i := 0; i < len(hard); i++ {
			if hard[i] != ' ' && hard[i] != '\t' {
				break
			}
			leading += string(hard[i])
		}
		if len(words) == 0 {
			out = append(out, leading)
			continue
		}
		var cur strings.Builder
		cur.WriteString(leading)
		curLen := len(leading)
		for i, word := range words {
			wlen := len(word)
			sep := 0
			if i > 0 && curLen > len(leading) {
				sep = 1
			}
			if curLen+sep+wlen > width && curLen > 0 {
				out = append(out, cur.String())
				cur.Reset()
				cur.WriteString(leading)
				curLen = len(leading)
				sep = 0
			}
			if sep == 1 {
				cur.WriteByte(' ')
				curLen++
			}
			cur.WriteString(word)
			curLen += wlen
		}
		if cur.Len() > 0 {
			out = append(out, cur.String())
		}
	}
	return out
}
