// Package store is an in-memory vector store with optional JSON persistence.
//
// Scale notes: holds everything in RAM. At 1024-dim float32 embeddings and
// ~1500-char chunks, ~50K chunks fits in ~200 MB. Beyond that, swap in
// sqlite-vec or LanceDB. v0 keeps it simple.
package store

import (
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// Doc is a single indexed chunk.
type Doc struct {
	Path  string    `json:"path"`        // path relative to data dir
	Start int       `json:"start"`       // byte offset within source file
	End   int       `json:"end"`         // exclusive end offset
	Chunk string    `json:"chunk"`       // raw text
	Vec   []float32 `json:"vec"`         // unit-normalized embedding
}

// Result is a search hit with its similarity score.
type Result struct {
	Doc   Doc
	Score float32
}

type Store struct {
	mu       sync.RWMutex
	docs     []Doc
	persist  string // file path; "" disables persistence
}

// Open loads any existing store from persistPath. Pass "" for ephemeral.
func Open(persistPath string) (*Store, error) {
	s := &Store{persist: persistPath}
	if persistPath == "" {
		return s, nil
	}
	if err := os.MkdirAll(filepath.Dir(persistPath), 0o755); err != nil {
		return nil, err
	}
	f, err := os.Open(persistPath)
	if errors.Is(err, os.ErrNotExist) {
		return s, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()
	if err := json.NewDecoder(f).Decode(&s.docs); err != nil {
		return nil, err
	}
	return s, nil
}

// Save flushes to disk if persistence is enabled.
func (s *Store) Save() error {
	if s.persist == "" {
		return nil
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	tmp := s.persist + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	if err := json.NewEncoder(f).Encode(s.docs); err != nil {
		_ = f.Close()
		_ = os.Remove(tmp)
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	return os.Rename(tmp, s.persist)
}

// Replace atomically swaps all docs for the given file path.
// Used after a file is (re)ingested.
func (s *Store) Replace(path string, docs []Doc) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(path)
	for _, d := range docs {
		d.Vec = unitNorm(d.Vec)
		s.docs = append(s.docs, d)
	}
}

// Remove drops all docs for a given path.
func (s *Store) Remove(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.removeLocked(path)
}

func (s *Store) removeLocked(path string) {
	out := s.docs[:0]
	for _, d := range s.docs {
		if d.Path != path {
			out = append(out, d)
		}
	}
	s.docs = out
}

// Paths returns sorted unique file paths currently indexed.
func (s *Store) Paths() []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	seen := make(map[string]struct{}, len(s.docs))
	for _, d := range s.docs {
		seen[d.Path] = struct{}{}
	}
	out := make([]string, 0, len(seen))
	for p := range seen {
		out = append(out, p)
	}
	sort.Strings(out)
	return out
}

// Has reports whether any chunks exist for path.
func (s *Store) Has(path string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, d := range s.docs {
		if d.Path == path {
			return true
		}
	}
	return false
}

// Search returns the top-k chunks by cosine similarity. queryVec is
// normalized internally; callers don't have to.
func (s *Store) Search(queryVec []float32, k int) []Result {
	if k <= 0 {
		k = 8
	}
	q := unitNorm(queryVec)
	s.mu.RLock()
	defer s.mu.RUnlock()

	results := make([]Result, 0, len(s.docs))
	for _, d := range s.docs {
		if len(d.Vec) != len(q) {
			continue
		}
		results = append(results, Result{Doc: d, Score: dot(q, d.Vec)})
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Score > results[j].Score })
	if len(results) > k {
		results = results[:k]
	}
	return results
}

func unitNorm(v []float32) []float32 {
	var sum float64
	for _, x := range v {
		sum += float64(x) * float64(x)
	}
	if sum == 0 {
		return v
	}
	inv := float32(1.0 / math.Sqrt(sum))
	out := make([]float32, len(v))
	for i, x := range v {
		out[i] = x * inv
	}
	return out
}

func dot(a, b []float32) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}
