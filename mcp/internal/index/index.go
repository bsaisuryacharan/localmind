// Package index manages the local file index that backs the MCP gateway.
//
// Watches DATA_DIR with fsnotify, ingests new/changed text files
// (.md, .txt, .markdown, .rst), embeds chunks via Ollama, and stores
// vectors in an in-memory store with optional JSON persistence to
// INDEX_DIR/index.json. Falls back to a periodic rescan if fsnotify
// initialization fails (e.g. on filesystems without inotify support).
//
// A "safety rescan" still runs every RescanInterval to catch events
// that fsnotify missed (buffer overflow, races on container start).
package index

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"

	"github.com/localmind/localmind/mcp/internal/embed"
	"github.com/localmind/localmind/mcp/internal/store"
)

// Config configures the Index.
type Config struct {
	DataDir        string
	IndexDir       string
	EmbeddingModel string
	OllamaBaseURL  string

	// Tunables. Zero values mean "use sensible default".
	RescanInterval time.Duration
	MaxFileBytes   int64
	ChunkBytes     int
	ChunkOverlap   int
}

const (
	defaultRescan       = 30 * time.Second
	defaultMaxFileBytes = 4 << 20 // 4 MB
	defaultChunkBytes   = 1500
	defaultChunkOverlap = 150
)

var indexableExtensions = map[string]bool{
	".md": true, ".markdown": true, ".txt": true, ".rst": true,
}

// fileMeta tracks what we last ingested for change detection.
type fileMeta struct {
	modTime time.Time
	size    int64
}

// Index is the long-running indexer.
type Index struct {
	cfg      Config
	store    *store.Store
	embedder *embed.Client

	mu       sync.Mutex
	known    map[string]fileMeta
	cancel   context.CancelFunc
	wg       sync.WaitGroup
}

// Open starts the indexer and returns once the first scan has been
// kicked off. The first scan runs in the background; queries before it
// completes will simply return fewer results.
func Open(parent context.Context, cfg Config) (*Index, error) {
	if cfg.RescanInterval == 0 {
		cfg.RescanInterval = defaultRescan
	}
	if cfg.MaxFileBytes == 0 {
		cfg.MaxFileBytes = defaultMaxFileBytes
	}
	if cfg.ChunkBytes == 0 {
		cfg.ChunkBytes = defaultChunkBytes
	}
	if cfg.ChunkOverlap == 0 {
		cfg.ChunkOverlap = defaultChunkOverlap
	}
	if cfg.DataDir == "" {
		return nil, errors.New("index: DataDir is required")
	}

	persistPath := ""
	if cfg.IndexDir != "" {
		persistPath = filepath.Join(cfg.IndexDir, "index.json")
	}
	st, err := store.Open(persistPath)
	if err != nil {
		return nil, fmt.Errorf("index: open store: %w", err)
	}

	ctx, cancel := context.WithCancel(parent)
	i := &Index{
		cfg:      cfg,
		store:    st,
		embedder: embed.New(cfg.OllamaBaseURL, cfg.EmbeddingModel),
		known:    make(map[string]fileMeta),
		cancel:   cancel,
	}

	// Seed `known` from already-persisted docs so we don't re-embed everything
	// on warm starts. We can't recover the exact mtime, but presence is enough
	// to skip until the file actually changes (we still re-check on size).
	for _, p := range st.Paths() {
		full := filepath.Join(cfg.DataDir, p)
		if fi, err := os.Stat(full); err == nil {
			i.known[p] = fileMeta{modTime: fi.ModTime(), size: fi.Size()}
		}
	}

	i.wg.Add(1)
	go i.loop(ctx)

	return i, nil
}

// Close stops the background loop and flushes the store to disk.
func (i *Index) Close() error {
	i.cancel()
	i.wg.Wait()
	return i.store.Save()
}

// loop is the indexer's main loop. It tries fsnotify first (incremental,
// near-real-time updates) and falls back to a pure periodic rescan if the
// watcher cannot be initialized.
func (i *Index) loop(ctx context.Context) {
	defer i.wg.Done()

	w, err := fsnotify.NewWatcher()
	if err != nil {
		log.Printf("index: fsnotify init failed (%v); falling back to periodic rescan", err)
		i.fallbackLoop(ctx)
		return
	}
	defer w.Close()

	if err := i.watchTree(w); err != nil {
		log.Printf("index: watchTree: %v", err)
	}

	// Always do a full scan at startup to seed / catch up.
	i.scan(ctx)

	// Safety rescan: catches events the watcher missed (buffer overflow,
	// boot races, removed-then-restored directories).
	safety := time.NewTicker(i.cfg.RescanInterval)
	defer safety.Stop()

	// Per-path debounce so editors that emit several Write events for one
	// save don't trigger N redundant ingestions.
	var (
		debounceMu sync.Mutex
		debounce   = map[string]*time.Timer{}
	)
	scheduleIngest := func(path string) {
		debounceMu.Lock()
		defer debounceMu.Unlock()
		if t, ok := debounce[path]; ok {
			t.Stop()
		}
		debounce[path] = time.AfterFunc(500*time.Millisecond, func() {
			i.handleFileChange(ctx, path)
			debounceMu.Lock()
			delete(debounce, path)
			debounceMu.Unlock()
		})
	}

	for {
		select {
		case <-ctx.Done():
			return

		case <-safety.C:
			i.scan(ctx)

		case ev, ok := <-w.Events:
			if !ok {
				return
			}
			i.handleEvent(ctx, w, ev, scheduleIngest)

		case err, ok := <-w.Errors:
			if !ok {
				return
			}
			log.Printf("index: watcher error: %v", err)
		}
	}
}

// fallbackLoop is the original periodic rescan. Used when fsnotify is
// unavailable (e.g. exotic filesystems without inotify).
func (i *Index) fallbackLoop(ctx context.Context) {
	t := time.NewTicker(i.cfg.RescanInterval)
	defer t.Stop()
	i.scan(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			i.scan(ctx)
		}
	}
}

// watchTree adds DataDir and every existing subdirectory to the watcher.
// fsnotify is non-recursive on every supported platform, so we walk.
func (i *Index) watchTree(w *fsnotify.Watcher) error {
	return filepath.WalkDir(i.cfg.DataDir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if err := w.Add(path); err != nil {
			log.Printf("index: watch add %s: %v", path, err)
		}
		return nil
	})
}

// handleEvent dispatches a single fsnotify event:
//   - Create on a directory: add to watcher and walk it for any pre-existing files.
//   - Remove / Rename on a known path: drop from store immediately.
//   - Create / Write on a file: schedule a debounced ingest.
func (i *Index) handleEvent(ctx context.Context, w *fsnotify.Watcher, ev fsnotify.Event, scheduleIngest func(string)) {
	if ev.Op.Has(fsnotify.Create) {
		if fi, err := os.Stat(ev.Name); err == nil && fi.IsDir() {
			if err := w.Add(ev.Name); err != nil {
				log.Printf("index: watch add %s: %v", ev.Name, err)
			}
			_ = filepath.WalkDir(ev.Name, func(p string, d fs.DirEntry, _ error) error {
				if d != nil && !d.IsDir() {
					scheduleIngest(p)
				}
				return nil
			})
			return
		}
	}

	if ev.Op.Has(fsnotify.Remove) || ev.Op.Has(fsnotify.Rename) {
		rel, err := filepath.Rel(i.cfg.DataDir, ev.Name)
		if err != nil {
			return
		}
		rel = filepath.ToSlash(rel)
		i.mu.Lock()
		i.store.Remove(rel)
		delete(i.known, rel)
		i.mu.Unlock()
		log.Printf("index: removed %s", rel)
		return
	}

	if ev.Op.Has(fsnotify.Create) || ev.Op.Has(fsnotify.Write) {
		scheduleIngest(ev.Name)
	}
}

// handleFileChange ingests a single file after its debounce window expires.
// Mirrors the per-file logic in scan() but for one path.
func (i *Index) handleFileChange(ctx context.Context, full string) {
	fi, err := os.Stat(full)
	if err != nil {
		return // file vanished; the Remove handler already cleaned up
	}
	if fi.IsDir() {
		return
	}
	if !indexableExtensions[strings.ToLower(filepath.Ext(full))] {
		return
	}
	if fi.Size() > i.cfg.MaxFileBytes {
		return
	}
	rel, err := filepath.Rel(i.cfg.DataDir, full)
	if err != nil {
		return
	}
	rel = filepath.ToSlash(rel)

	i.mu.Lock()
	prev, had := i.known[rel]
	i.mu.Unlock()
	if had && prev.modTime.Equal(fi.ModTime()) && prev.size == fi.Size() {
		return // already up to date
	}

	if err := i.ingest(ctx, rel, full); err != nil {
		log.Printf("index: ingest %s: %v", rel, err)
		return
	}
	i.mu.Lock()
	i.known[rel] = fileMeta{modTime: fi.ModTime(), size: fi.Size()}
	i.mu.Unlock()
	if err := i.store.Save(); err != nil {
		log.Printf("index: save: %v", err)
	}
}

// scan walks DataDir and ingests new or modified files. Deletes are
// detected by tracking the set of paths seen this pass.
func (i *Index) scan(ctx context.Context) {
	seen := make(map[string]struct{}, len(i.known))
	err := filepath.WalkDir(i.cfg.DataDir, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil || d.IsDir() {
			return nil
		}
		if !indexableExtensions[strings.ToLower(filepath.Ext(path))] {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		if fi.Size() > i.cfg.MaxFileBytes {
			return nil
		}
		rel, err := filepath.Rel(i.cfg.DataDir, path)
		if err != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		seen[rel] = struct{}{}

		i.mu.Lock()
		prev, had := i.known[rel]
		i.mu.Unlock()
		if had && prev.modTime.Equal(fi.ModTime()) && prev.size == fi.Size() {
			return nil
		}

		if err := i.ingest(ctx, rel, path); err != nil {
			log.Printf("index: ingest %s: %v", rel, err)
			return nil
		}
		i.mu.Lock()
		i.known[rel] = fileMeta{modTime: fi.ModTime(), size: fi.Size()}
		i.mu.Unlock()
		return nil
	})
	if err != nil {
		log.Printf("index: walk: %v", err)
	}

	// Deletes
	i.mu.Lock()
	for path := range i.known {
		if _, ok := seen[path]; ok {
			continue
		}
		i.store.Remove(path)
		delete(i.known, path)
		log.Printf("index: removed %s", path)
	}
	i.mu.Unlock()

	if err := i.store.Save(); err != nil {
		log.Printf("index: save: %v", err)
	}
}

// ingest reads, chunks, embeds, and replaces a single file's docs.
func (i *Index) ingest(ctx context.Context, rel, full string) error {
	body, err := os.ReadFile(full)
	if err != nil {
		return err
	}
	if isBinary(body) {
		return nil
	}
	chunks := chunk(string(body), i.cfg.ChunkBytes, i.cfg.ChunkOverlap)
	if len(chunks) == 0 {
		return nil
	}

	docs := make([]store.Doc, 0, len(chunks))
	for _, c := range chunks {
		vec, err := i.embedder.Embed(ctx, c.text)
		if err != nil {
			return err
		}
		docs = append(docs, store.Doc{
			Path: rel, Start: c.start, End: c.end, Chunk: c.text, Vec: vec,
		})
	}
	i.store.Replace(rel, docs)
	log.Printf("index: ingested %s (%d chunks)", rel, len(docs))
	return nil
}

// Search returns top-k matches for query.
func (i *Index) Search(ctx context.Context, query string, k int) ([]store.Result, error) {
	q, err := i.embedder.Embed(ctx, query)
	if err != nil {
		return nil, err
	}
	return i.store.Search(q, k), nil
}

// List returns indexed paths.
func (i *Index) List() []string { return i.store.Paths() }

// Read returns the full contents of an indexed file (re-read from disk).
func (i *Index) Read(rel string) (string, error) {
	if !i.store.Has(rel) {
		return "", fmt.Errorf("not indexed: %s", rel)
	}
	full := filepath.Join(i.cfg.DataDir, filepath.FromSlash(rel))
	body, err := os.ReadFile(full)
	if err != nil {
		return "", err
	}
	return string(body), nil
}

// ToolDescriptors returns MCP tool descriptors that this index exposes.
func (i *Index) ToolDescriptors() []map[string]any {
	return []map[string]any{
		{
			"name":        "search_files",
			"description": "Semantic search over files in the localmind data directory.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"query": map[string]any{"type": "string"},
					"k":     map[string]any{"type": "integer", "default": 8},
				},
				"required": []string{"query"},
			},
		},
		{
			"name":        "list_files",
			"description": "List files currently known to the localmind index.",
			"inputSchema": map[string]any{"type": "object"},
		},
		{
			"name":        "read_file",
			"description": "Return the full contents of an indexed file.",
			"inputSchema": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
				"required": []string{"path"},
			},
		},
	}
}

// chunkSpan tracks the byte range a chunk came from.
type chunkSpan struct {
	start, end int
	text       string
}

// chunk splits text into overlapping byte-bounded windows, preferring
// to break on newlines. Naive but predictable.
func chunk(text string, size, overlap int) []chunkSpan {
	if size <= 0 {
		return nil
	}
	if overlap >= size {
		overlap = size / 4
	}
	var out []chunkSpan
	n := len(text)
	for start := 0; start < n; {
		end := start + size
		if end >= n {
			out = append(out, chunkSpan{start, n, text[start:n]})
			break
		}
		// Try to land on a newline within the last 25% of the window
		// for cleaner chunk boundaries.
		minBack := end - size/4
		for i := end; i > minBack; i-- {
			if text[i-1] == '\n' {
				end = i
				break
			}
		}
		out = append(out, chunkSpan{start, end, text[start:end]})
		next := end - overlap
		if next <= start {
			next = end
		}
		start = next
	}
	return out
}

// isBinary returns true if the first 512 bytes contain a NUL.
func isBinary(b []byte) bool {
	n := len(b)
	if n > 512 {
		n = 512
	}
	for i := 0; i < n; i++ {
		if b[i] == 0 {
			return true
		}
	}
	return false
}
