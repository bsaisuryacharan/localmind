// localmind-mcp is the local MCP gateway.
//
// It exposes two surfaces:
//
//   1. /mcp           Streamable HTTP MCP endpoint (search, list_tools, ...).
//   2. /healthz       Liveness for `localmind status`.
//
// Tools (v0):
//
//   search_files     semantic search over the watched data dir
//   list_files       list files known to the index
//   read_file        return the contents of an indexed file
//
// The index is maintained by internal/index, which periodically rescans
// $DATA_DIR and embeds new/changed files via Ollama's /api/embeddings.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/localmind/localmind/mcp/internal/index"
)

func main() {
	cfg := loadConfig()
	log.Printf("localmind-mcp starting: addr=%s data=%s index=%s ollama=%s",
		cfg.Addr, cfg.DataDir, cfg.IndexDir, cfg.OllamaBaseURL)

	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()

	idx, err := index.Open(ctx, index.Config{
		DataDir:        cfg.DataDir,
		IndexDir:       cfg.IndexDir,
		EmbeddingModel: cfg.EmbeddingModel,
		OllamaBaseURL:  cfg.OllamaBaseURL,
	})
	if err != nil {
		log.Fatalf("open index: %v", err)
	}
	defer idx.Close()

	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("ok"))
	})
	mux.Handle("/mcp", newMCPHandler(idx))

	srv := &http.Server{
		Addr:              cfg.Addr,
		Handler:           mux,
		ReadHeaderTimeout: 5 * time.Second,
	}

	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		<-ctx.Done()
		shutdownCtx, c := context.WithTimeout(context.Background(), 10*time.Second)
		defer c()
		_ = srv.Shutdown(shutdownCtx)
	}()

	if err := srv.ListenAndServe(); !errors.Is(err, http.ErrServerClosed) {
		log.Fatalf("listen: %v", err)
	}
	wg.Wait()
}

type config struct {
	Addr           string
	DataDir        string
	IndexDir       string
	EmbeddingModel string
	OllamaBaseURL  string
}

func loadConfig() config {
	return config{
		Addr:           getenv("LOCALMIND_ADDR", ":7800"),
		DataDir:        getenv("DATA_DIR", "/data"),
		IndexDir:       getenv("INDEX_DIR", "/var/lib/localmind"),
		EmbeddingModel: getenv("EMBEDDING_MODEL", "nomic-embed-text"),
		OllamaBaseURL:  getenv("OLLAMA_BASE_URL", "http://ollama:11434"),
	}
}

func getenv(k, def string) string {
	if v, ok := os.LookupEnv(k); ok {
		return v
	}
	return def
}

// MCP JSON-RPC error codes. Values track the MCP spec.
const (
	rpcInvalidRequest = -32600
	rpcMethodNotFound = -32601
	rpcInvalidParams  = -32602
	rpcInternalError  = -32603
)

type rpcReq struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      any             `json:"id"`
	Method  string          `json:"method"`
	Params  json.RawMessage `json:"params"`
}

type rpcErr struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

type rpcResp struct {
	JSONRPC string  `json:"jsonrpc"`
	ID      any     `json:"id"`
	Result  any     `json:"result,omitempty"`
	Error   *rpcErr `json:"error,omitempty"`
}

// newMCPHandler dispatches MCP JSON-RPC requests against the index.
//
// Speaks the methods Claude Code currently uses: initialize, tools/list,
// tools/call. Unknown methods get a method-not-found error.
func newMCPHandler(idx *index.Index) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req rpcReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeRPCError(w, nil, rpcInvalidRequest, fmt.Sprintf("bad json: %v", err))
			return
		}

		switch req.Method {
		case "initialize":
			writeRPCResult(w, req.ID, map[string]any{
				"protocolVersion": "2025-03-26",
				"serverInfo":      map[string]any{"name": "localmind-mcp", "version": "0.0.1"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			})
		case "tools/list":
			writeRPCResult(w, req.ID, map[string]any{"tools": idx.ToolDescriptors()})
		case "tools/call":
			handleToolCall(r.Context(), w, req, idx)
		case "notifications/initialized", "notifications/cancelled":
			// MCP notifications: no response expected.
			w.WriteHeader(http.StatusNoContent)
		default:
			writeRPCError(w, req.ID, rpcMethodNotFound, "unknown method: "+req.Method)
		}
	})
}

func handleToolCall(ctx context.Context, w http.ResponseWriter, req rpcReq, idx *index.Index) {
	var p struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}
	if err := json.Unmarshal(req.Params, &p); err != nil {
		writeRPCError(w, req.ID, rpcInvalidParams, err.Error())
		return
	}

	switch p.Name {
	case "search_files":
		var args struct {
			Query string `json:"query"`
			K     int    `json:"k"`
		}
		_ = json.Unmarshal(p.Arguments, &args)
		if args.Query == "" {
			writeRPCError(w, req.ID, rpcInvalidParams, "query is required")
			return
		}
		results, err := idx.Search(ctx, args.Query, args.K)
		if err != nil {
			writeRPCError(w, req.ID, rpcInternalError, err.Error())
			return
		}
		var sb strings.Builder
		if len(results) == 0 {
			sb.WriteString("(no results)")
		}
		for i, r := range results {
			fmt.Fprintf(&sb, "## Result %d  %s  (score=%.3f, bytes %d-%d)\n%s\n\n",
				i+1, r.Doc.Path, r.Score, r.Doc.Start, r.Doc.End, r.Doc.Chunk)
		}
		writeRPCResult(w, req.ID, mcpTextContent(sb.String()))

	case "list_files":
		paths := idx.List()
		text := strings.Join(paths, "\n")
		if text == "" {
			text = "(index empty)"
		}
		writeRPCResult(w, req.ID, mcpTextContent(text))

	case "read_file":
		var args struct {
			Path string `json:"path"`
		}
		_ = json.Unmarshal(p.Arguments, &args)
		if args.Path == "" {
			writeRPCError(w, req.ID, rpcInvalidParams, "path is required")
			return
		}
		body, err := idx.Read(args.Path)
		if err != nil {
			writeRPCError(w, req.ID, rpcInternalError, err.Error())
			return
		}
		writeRPCResult(w, req.ID, mcpTextContent(body))

	default:
		writeRPCError(w, req.ID, rpcMethodNotFound, "unknown tool: "+p.Name)
	}
}

// mcpTextContent wraps a string in the MCP ToolResult schema.
func mcpTextContent(s string) map[string]any {
	return map[string]any{
		"content": []any{map[string]any{"type": "text", "text": s}},
	}
}

func writeRPCResult(w http.ResponseWriter, id any, result any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcResp{JSONRPC: "2.0", ID: id, Result: result})
}

func writeRPCError(w http.ResponseWriter, id any, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rpcResp{
		JSONRPC: "2.0", ID: id,
		Error: &rpcErr{Code: code, Message: msg},
	})
}
