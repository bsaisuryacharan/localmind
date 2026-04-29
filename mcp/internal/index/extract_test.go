package index

import (
	"archive/zip"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestExtractText_MarkdownVerbatim(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "note.md")
	body := "# heading\n\nBody text with *italics*.\n"
	writeFile(t, path, body)

	got, err := extractText(path)
	if err != nil {
		t.Fatalf("extractText: %v", err)
	}
	if got != body {
		t.Fatalf("extractText(.md)=%q want %q", got, body)
	}
}

func TestExtractText_TxtVerbatim(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "log.txt")
	body := "line one\nline two\n"
	writeFile(t, path, body)

	got, err := extractText(path)
	if err != nil {
		t.Fatalf("extractText: %v", err)
	}
	if got != body {
		t.Fatalf("extractText(.txt)=%q want %q", got, body)
	}
}

func TestExtractText_BinaryFileReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "looks-textual.txt")
	// NUL byte early enough to land in the 512-byte sniff window.
	body := []byte{'h', 'i', 0, 'x', 'y', 'z'}
	if err := os.WriteFile(path, body, 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	got, err := extractText(path)
	if err != nil {
		t.Fatalf("extractText: %v", err)
	}
	if got != "" {
		t.Fatalf("extractText(binary .txt)=%q want \"\"", got)
	}
}

func TestExtractText_UnknownExtensionReturnsEmpty(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "binary.exe")
	writeFile(t, path, "MZ\x90\x00")

	got, err := extractText(path)
	if err != nil {
		t.Fatalf("extractText: %v", err)
	}
	if got != "" {
		t.Fatalf("extractText(.exe)=%q want \"\"", got)
	}
}

// writeMinimalDocx assembles a stdlib-only DOCX containing a single <w:t>
// run. The DOCX format is a zip with at least [Content_Types].xml and a
// document part; extractDOCX only reads word/document.xml so we ship the
// bare minimum here.
func writeMinimalDocx(t *testing.T, path, text string) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatalf("create docx: %v", err)
	}
	defer f.Close()
	zw := zip.NewWriter(f)
	defer zw.Close()

	addFile := func(name, content string) {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatalf("zip create %s: %v", name, err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatalf("zip write %s: %v", name, err)
		}
	}

	contentTypes := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<Types xmlns="http://schemas.openxmlformats.org/package/2006/content-types">
  <Default Extension="xml" ContentType="application/xml"/>
  <Override PartName="/word/document.xml" ContentType="application/vnd.openxmlformats-officedocument.wordprocessingml.document.main+xml"/>
</Types>`
	addFile("[Content_Types].xml", contentTypes)

	document := `<?xml version="1.0" encoding="UTF-8" standalone="yes"?>
<w:document xmlns:w="http://schemas.openxmlformats.org/wordprocessingml/2006/main">
  <w:body>
    <w:p><w:r><w:t>` + text + `</w:t></w:r></w:p>
  </w:body>
</w:document>`
	addFile("word/document.xml", document)
}

func TestExtractText_DocxBasic(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.docx")
	writeMinimalDocx(t, path, "hello")

	got, err := extractText(path)
	if err != nil {
		t.Fatalf("extractText(.docx): %v", err)
	}
	if !strings.Contains(got, "hello") {
		t.Fatalf("extractText(.docx)=%q does not contain %q", got, "hello")
	}
}

// TestExtractText_PDF is intentionally skipped. Generating a valid PDF in
// pure stdlib (no third-party PDF writer) is a non-trivial binary-format
// exercise and well outside the value-per-line budget of this v1 suite.
// PDF parsing is exercised by the integration story; if a regression here
// matters we'll add a fixture under testdata/.
func TestExtractText_PDF(t *testing.T) {
	t.Skip("PDF extraction not unit-tested; would require hand-crafted PDF bytes")
}

// TestExtractText_OCRFallback is intentionally skipped. The OCR sidecar is
// reached over HTTP using a package-level ocrBaseURL var read at init time
// from os.Getenv; mocking that would require either changing the source
// or running the OCR sidecar. Out of scope for v1.
func TestExtractText_OCRFallback(t *testing.T) {
	t.Skip("OCR fallback path not unit-tested; ocrBaseURL is read at init from env")
}
