package index

import (
	"strings"
	"testing"
	"time"
)

func TestChunk_SmallerThanWindowReturnsOne(t *testing.T) {
	t.Parallel()
	in := "short text"
	got := chunk(in, 1500, 150)
	if len(got) != 1 {
		t.Fatalf("len(chunks)=%d want 1", len(got))
	}
	if got[0].start != 0 || got[0].end != len(in) {
		t.Fatalf("span=[%d,%d) want [0,%d)", got[0].start, got[0].end, len(in))
	}
	if got[0].text != in {
		t.Fatalf("text mismatch")
	}
}

func TestChunk_RespectsByteSize(t *testing.T) {
	t.Parallel()
	// 10000 bytes of 'a' — no newlines so the boundary search is moot;
	// chunks should be exactly chunkBytes-overlap=1350 apart.
	in := strings.Repeat("a", 10000)
	got := chunk(in, 1500, 150)
	// ceil((10000-1500)/(1500-150)) + 1 == ceil(8500/1350) + 1 == 7+1 == 8
	// Allow ±1 because of the exact-length tail handling.
	if len(got) < 7 || len(got) > 9 {
		t.Fatalf("len(chunks)=%d, want approx ceil((10000-150)/(1500-150)) ≈ 8", len(got))
	}
	// First chunk must start at 0; last chunk's end must be n.
	if got[0].start != 0 {
		t.Fatalf("first chunk start=%d want 0", got[0].start)
	}
	if got[len(got)-1].end != len(in) {
		t.Fatalf("last chunk end=%d want %d", got[len(got)-1].end, len(in))
	}
}

func TestChunk_PrefersNewlineBoundaries(t *testing.T) {
	t.Parallel()
	// Build text where a newline sits in the last quarter of a 100-byte
	// window. minBack = 100 - 25 = 75; place \n at byte 90.
	pre := strings.Repeat("a", 90)
	post := strings.Repeat("b", 200)
	in := pre + "\n" + post
	got := chunk(in, 100, 10)
	if len(got) == 0 {
		t.Fatalf("no chunks produced")
	}
	// First chunk should end on the newline (i.e., end == 91 because the
	// loop sets end = i where text[i-1] == '\n').
	if got[0].end != 91 {
		t.Fatalf("first chunk end=%d want 91 (newline boundary)", got[0].end)
	}
}

func TestChunk_OverlapDoesNotInfiniteLoop(t *testing.T) {
	t.Parallel()
	// overlap == size used to be a hang risk. The implementation
	// halves overlap when overlap >= size, so this should terminate.
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = chunk(strings.Repeat("x", 5000), 100, 100)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("chunk(overlap==size) did not terminate within 2s")
	}
}

func TestIsBinary_NULBytePrefix(t *testing.T) {
	t.Parallel()
	if !isBinary([]byte{'a', 'b', 0, 'c'}) {
		t.Fatalf("isBinary did not detect NUL in first 512 bytes")
	}
	if isBinary([]byte("plain ASCII content with no nulls")) {
		t.Fatalf("isBinary flagged pure ASCII as binary")
	}
	// NUL beyond the 512-byte sniff window must NOT count as binary
	// (consistent with the documented behavior).
	pad := make([]byte, 600)
	for i := range pad[:512] {
		pad[i] = 'a'
	}
	pad[550] = 0
	if isBinary(pad) {
		t.Fatalf("isBinary flagged file with NUL outside sniff window")
	}
}

func TestIndexableExtensions_PositiveAndNegative(t *testing.T) {
	t.Parallel()
	// Positive cases for the lower-cased extensions.
	for _, ext := range []string{".md", ".markdown", ".txt", ".rst", ".pdf", ".docx"} {
		if !indexableExtensions[ext] {
			t.Fatalf("indexableExtensions[%q]=false want true", ext)
		}
	}
	// Negative case.
	if indexableExtensions[".exe"] {
		t.Fatalf("indexableExtensions[\".exe\"]=true want false")
	}
	// The map is keyed by lowercase. Callers normalize via strings.ToLower
	// before lookup; this test mirrors that contract.
	if !indexableExtensions[lower(".PDF")] {
		t.Fatalf(".PDF (after ToLower) did not match indexableExtensions")
	}
}

func lower(s string) string { return strings.ToLower(s) }
