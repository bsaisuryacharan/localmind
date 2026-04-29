package index

import (
	"archive/zip"
	"bytes"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/ledongthuc/pdf"
)

// ocrBaseURL is the OCR sidecar's HTTP endpoint. Set via the OCR_BASE_URL
// env var; empty disables OCR fallback for local builds without docker.
var ocrBaseURL = os.Getenv("OCR_BASE_URL")

// ocrClient is shared so connection reuse helps with multi-PDF batches.
var ocrClient = &http.Client{Timeout: 5 * time.Minute}

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
// preserved only loosely: pages are joined with a blank line. If the
// extracted text is empty (image-only / scanned PDF) and an OCR sidecar
// is configured via OCR_BASE_URL, fall through to ocrPDF.
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
	out := sb.String()
	if strings.TrimSpace(out) != "" || ocrBaseURL == "" {
		return out, nil
	}
	// Text layer was empty and OCR is configured; fall back. ocrPDF
	// returns ("", err) on failure; we propagate so the caller can log
	// and skip the file rather than silently indexing nothing.
	return ocrPDF(path)
}

// ocrPDF POSTs the file to the OCR sidecar and returns the recovered
// plain text. The sidecar runs ocrmypdf + pdftotext under the hood;
// see ocrmypdf/server.py.
func ocrPDF(path string) (string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("ocr: read pdf: %w", err)
	}
	url := strings.TrimRight(ocrBaseURL, "/") + "/v1/ocr"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return "", fmt.Errorf("ocr: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/pdf")
	resp, err := ocrClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("ocr: post: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		msg, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<14))
		return "", fmt.Errorf("ocr: %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	text, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("ocr: read response: %w", err)
	}
	return string(text), nil
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
