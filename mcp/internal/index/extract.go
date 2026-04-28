package index

import (
	"archive/zip"
	"encoding/xml"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/ledongthuc/pdf"
)

// extractText returns the plain-text content of a file, dispatching by
// extension. The returned string is what gets chunked + embedded.
//
// Supported types:
//
//	.md, .markdown, .txt, .rst   verbatim file contents
//	.pdf                          page-by-page text via ledongthuc/pdf
//	.docx                         <w:t> runs inside word/document.xml
//
// Anything else returns an empty string and a nil error so callers can
// silently skip it. A non-nil error means the file *should* have been
// extractable but something went wrong (corrupt PDF, unzip failure).
func extractText(path string) (string, error) {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".md", ".markdown", ".txt", ".rst":
		body, err := os.ReadFile(path)
		if err != nil {
			return "", err
		}
		if isBinary(body) {
			return "", nil
		}
		return string(body), nil

	case ".pdf":
		return extractPDF(path)

	case ".docx":
		return extractDOCX(path)
	}
	return "", nil
}

// extractPDF reads every page and concatenates the plain text. Layout is
// preserved only loosely: pages are joined with a blank line.
func extractPDF(path string) (string, error) {
	f, r, err := pdf.Open(path)
	if err != nil {
		return "", fmt.Errorf("open pdf: %w", err)
	}
	defer f.Close()

	var sb strings.Builder
	pages := r.NumPage()
	for i := 1; i <= pages; i++ {
		page := r.Page(i)
		if page.V.IsNull() {
			continue
		}
		text, err := page.GetPlainText(nil)
		if err != nil {
			// One bad page shouldn't take down the whole document.
			continue
		}
		sb.WriteString(text)
		sb.WriteString("\n\n")
	}
	return sb.String(), nil
}

// docxBody is the minimal XML schema we need to walk in word/document.xml.
// We only care about text runs (<w:t>) and paragraph boundaries (<w:p>).
type docxBody struct {
	Paragraphs []docxPara `xml:"body>p"`
}

type docxPara struct {
	Runs []docxRun `xml:"r"`
}

type docxRun struct {
	Texts []string `xml:"t"`
}

// extractDOCX unzips the document and walks word/document.xml for <w:t>
// runs. Stdlib only — no third-party DOCX library required.
func extractDOCX(path string) (string, error) {
	zr, err := zip.OpenReader(path)
	if err != nil {
		return "", fmt.Errorf("open docx: %w", err)
	}
	defer zr.Close()

	var doc *zip.File
	for _, f := range zr.File {
		if f.Name == "word/document.xml" {
			doc = f
			break
		}
	}
	if doc == nil {
		return "", fmt.Errorf("docx missing word/document.xml")
	}

	rc, err := doc.Open()
	if err != nil {
		return "", err
	}
	defer rc.Close()

	body, err := io.ReadAll(rc)
	if err != nil {
		return "", err
	}

	// The full schema is hairy. We only need <w:t> contents in document
	// order, so a streaming token decoder is cheaper than full unmarshal.
	dec := xml.NewDecoder(strings.NewReader(string(body)))
	var sb strings.Builder
	inText := false
	for {
		tok, err := dec.Token()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("parse docx: %w", err)
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if t.Name.Local == "t" { // <w:t>
				inText = true
			}
			if t.Name.Local == "p" { // <w:p> — paragraph boundary
				sb.WriteString("\n")
			}
		case xml.EndElement:
			if t.Name.Local == "t" {
				inText = false
			}
		case xml.CharData:
			if inText {
				sb.Write(t)
			}
		}
	}
	return sb.String(), nil
}
