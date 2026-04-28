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
// The index is built by internal/index by tailing fsnotify events on
// $DATA_DIR and embedding new/changed files via Ollama's /api/embeddings.
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

// newMCPHandler returns a stub HTTP handler that speaks just enough of MCP
// to respond to `initialize` and `tools/list` requests. The full protocol
// implementation will replace this once the Go MCP SDK is wired up.
func newMCPHandler(idx *index.Index) http.Handler {
	type rpcReq struct {
		ID     any             `json:"id"`
		Method string          `json:"method"`
		Params json.RawMessage `json:"params"`
	}
	type rpcResp struct {
		ID     any `json:"id"`
		Result any `json:"result,omitempty"`
		Error  any `json:"error,omitempty"`
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "POST only", http.StatusMethodNotAllowed)
			return
		}
		var req rpcReq
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, fmt.Sprintf("bad json: %v", err), http.StatusBadRequest)
			return
		}

		var result any
		switch req.Method {
		case "initialize":
			result = map[string]any{
				"protocolVersion": "2025-03-26",
				"serverInfo":      map[string]any{"name": "localmind-mcp", "version": "0.0.1"},
				"capabilities":    map[string]any{"tools": map[string]any{}},
			}
		case "tools/list":
			result = map[string]any{"tools": idx.ToolDescriptors()}
		case "tools/call":
			result = map[string]any{"content": []any{map[string]any{"type": "text", "text": "TODO"}}}
		default:
			http.Error(w, "unknown method", http.StatusBadRequest)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(rpcResp{ID: req.ID, Result: result})
	})
}
