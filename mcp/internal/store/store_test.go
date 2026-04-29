package store

import (
	"math"
	"path/filepath"
	"reflect"
	"testing"
)

// approxEq compares two float32s with a small tolerance. Cosine math through
// unit-norm + dot is exact in principle but float rounding still bites.
func approxEq(a, b float32, tol float32) bool {
	d := a - b
	if d < 0 {
		d = -d
	}
	return d <= tol
}

func TestMemoryStore_Replace_AddsAndOverwrites(t *testing.T) {
	t.Parallel()
	s, err := NewMemoryStore("")
	if err != nil {
		t.Fatalf("NewMemoryStore: %v", err)
	}
	s.Replace("a.md", []Doc{
		{Path: "a.md", Start: 0, End: 5, Chunk: "hello", Vec: []float32{1, 0, 0}},
		{Path: "a.md", Start: 5, End: 10, Chunk: "world", Vec: []float32{0, 1, 0}},
	})
	if got := len(s.docs); got != 2 {
		t.Fatalf("after first Replace, len(docs)=%d want 2", got)
	}

	// Replacing the same path swaps in fresh docs and drops the old ones.
	s.Replace("a.md", []Doc{
		{Path: "a.md", Start: 0, End: 4, Chunk: "yolo", Vec: []float32{0, 0, 1}},
	})
	if got := len(s.docs); got != 1 {
		t.Fatalf("after overwrite, len(docs)=%d want 1", got)
	}
	if s.docs[0].Chunk != "yolo" {
		t.Fatalf("after overwrite, chunk=%q want %q", s.docs[0].Chunk, "yolo")
	}
}

func TestMemoryStore_Remove_DropsByPath(t *testing.T) {
	t.Parallel()
	s, _ := NewMemoryStore("")
	s.Replace("a.md", []Doc{{Path: "a.md", Vec: []float32{1, 0, 0}}})
	s.Replace("b.md", []Doc{{Path: "b.md", Vec: []float32{0, 1, 0}}})
	s.Remove("a.md")
	if s.Has("a.md") {
		t.Fatalf("Has(a.md) = true after Remove")
	}
	if !s.Has("b.md") {
		t.Fatalf("Has(b.md) = false; remove dropped the wrong path")
	}
}

func TestMemoryStore_Search_RanksByCosine(t *testing.T) {
	t.Parallel()
	s, _ := NewMemoryStore("")
	s.Replace("a.md", []Doc{{Path: "a.md", Chunk: "cat", Vec: []float32{1, 0, 0}}})
	s.Replace("b.md", []Doc{{Path: "b.md", Chunk: "dog", Vec: []float32{0, 1, 0}}})

	// Query much closer to a.md's vector.
	got := s.Search([]float32{0.99, 0.01, 0}, 2)
	if len(got) != 2 {
		t.Fatalf("len(results)=%d want 2", len(got))
	}
	if got[0].Doc.Path != "a.md" {
		t.Fatalf("top hit path=%q want a.md", got[0].Doc.Path)
	}
	if got[0].Score <= got[1].Score {
		t.Fatalf("expected descending scores; got %v then %v", got[0].Score, got[1].Score)
	}
}

func TestMemoryStore_Search_FiltersDimensionMismatch(t *testing.T) {
	t.Parallel()
	s, _ := NewMemoryStore("")
	// Doc with len(Vec)=3 cannot be compared against a query of len 5.
	s.Replace("a.md", []Doc{{Path: "a.md", Vec: []float32{1, 0, 0}}})
	got := s.Search([]float32{1, 0, 0, 0, 0}, 5)
	if len(got) != 0 {
		t.Fatalf("expected 0 results, got %d (mismatched dimensions should be skipped)", len(got))
	}
}

func TestMemoryStore_Search_RespectsK(t *testing.T) {
	t.Parallel()
	s, _ := NewMemoryStore("")
	for _, name := range []string{"a", "b", "c", "d", "e"} {
		s.Replace(name+".md", []Doc{{Path: name + ".md", Vec: []float32{1, 0, 0}}})
	}
	got := s.Search([]float32{1, 0, 0}, 2)
	if len(got) != 2 {
		t.Fatalf("k=2 returned %d results", len(got))
	}
}

func TestMemoryStore_Paths_DeduplicatesAndSorts(t *testing.T) {
	t.Parallel()
	s, _ := NewMemoryStore("")
	// Two chunks of the same path: Paths should still report it once.
	s.Replace("z.md", []Doc{
		{Path: "z.md", Vec: []float32{1, 0, 0}},
		{Path: "z.md", Vec: []float32{0, 1, 0}},
	})
	s.Replace("a.md", []Doc{{Path: "a.md", Vec: []float32{1, 0, 0}}})
	got := s.Paths()
	want := []string{"a.md", "z.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Paths()=%v want %v", got, want)
	}
}

func TestMemoryStore_PersistsRoundTrip(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	persist := filepath.Join(dir, "sub", "index.json")

	s1, err := NewMemoryStore(persist)
	if err != nil {
		t.Fatalf("Open#1: %v", err)
	}
	s1.Replace("a.md", []Doc{{Path: "a.md", Start: 0, End: 5, Chunk: "hello", Vec: []float32{1, 0, 0}}})
	if err := s1.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Re-open the same persist path.
	s2, err := NewMemoryStore(persist)
	if err != nil {
		t.Fatalf("Open#2: %v", err)
	}
	if !s2.Has("a.md") {
		t.Fatalf("after reopen, Has(a.md)=false; persistence didn't survive")
	}
	got := s2.Paths()
	if len(got) != 1 || got[0] != "a.md" {
		t.Fatalf("after reopen, Paths()=%v want [a.md]", got)
	}
}

func TestUnitNorm_ZeroVectorIsZeroSafe(t *testing.T) {
	t.Parallel()
	// Empty input shouldn't panic and should round-trip empty.
	if got := unitNorm(nil); len(got) != 0 {
		t.Fatalf("unitNorm(nil)=%v want empty", got)
	}
	if got := unitNorm([]float32{}); len(got) != 0 {
		t.Fatalf("unitNorm([])=%v want empty", got)
	}
	// All-zero input: returns the same vector (sentinel for "no signal").
	got := unitNorm([]float32{0, 0, 0})
	for _, v := range got {
		if v != 0 {
			t.Fatalf("unitNorm of all-zeros produced non-zero element %v", v)
		}
	}
}

func TestDot_BasicOrthogonal(t *testing.T) {
	t.Parallel()
	if got := dot([]float32{1, 0, 0}, []float32{0, 1, 0}); got != 0 {
		t.Fatalf("dot of orthogonal = %v want 0", got)
	}
	if got := dot([]float32{1, 0, 0}, []float32{1, 0, 0}); got != 1 {
		t.Fatalf("dot of parallel unit = %v want 1", got)
	}
	// Mixed values: 1*4 + 2*5 + 3*6 = 32
	if got := dot([]float32{1, 2, 3}, []float32{4, 5, 6}); !approxEq(got, 32, 1e-5) {
		t.Fatalf("dot([1,2,3],[4,5,6])=%v want 32", got)
	}
}

func TestStore_OpenReturnsInterface(t *testing.T) {
	t.Parallel()
	s, err := Open("")
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	if _, ok := s.(*MemoryStore); !ok {
		t.Fatalf("Open() did not return *MemoryStore; got %T", s)
	}
}

// TestUnitNorm_NormalizesToUnitLength sanity-checks that a non-trivial vector
// comes out with magnitude 1. Not in the explicit task list but it's the
// whole point of the function and the test is two lines.
func TestUnitNorm_NormalizesToUnitLength(t *testing.T) {
	t.Parallel()
	got := unitNorm([]float32{3, 4, 0}) // magnitude 5
	var sum float64
	for _, v := range got {
		sum += float64(v) * float64(v)
	}
	if math.Abs(math.Sqrt(sum)-1.0) > 1e-5 {
		t.Fatalf("unitNorm result has magnitude %v want 1.0", math.Sqrt(sum))
	}
}
