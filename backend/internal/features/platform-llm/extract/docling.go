package extract

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

// Docling posts files to a Docling conversion service.
type Docling struct{ b httpBase }

// NewDocling returns a Docling engine for the full endpoint URL.
func NewDocling(h Doer, url, token string, timeout time.Duration) *Docling {
	return &Docling{b: httpBase{engine: "docling", baseURL: strings.TrimRight(url, "/"), token: token, timeout: timeout, http: h, kind: outbound.KindTrusted}}
}

// Name implements Engine.
func (d *Docling) Name() string { return "docling" }

// Extract posts multipart (file, file_name, mime_type) and reads flexible JSON.
func (d *Docling) Extract(ctx context.Context, r Request) (*Result, error) {
	ctx, cancel := d.b.ctx(ctx)
	defer cancel()
	req, err := d.b.multipart(ctx, d.b.baseURL, map[string]string{"file_name": r.Name, "mime_type": r.MIME}, "file", r)
	if err != nil {
		return nil, coded("docling_invalid_file_path", err.Error())
	}
	status, body, _, err := d.b.do(ctx, req)
	if err != nil {
		return nil, err
	}
	if status/100 != 2 {
		return nil, d.b.statusError(status, body, false)
	}
	text, pages := parseFlexible(body)
	text = NormalizeText(text)
	if text == "" {
		return nil, coded("docling_empty_content", "")
	}
	return &Result{Text: text, PageTexts: pages, Pages: len(pages), Engine: "docling"}, nil
}

// healthURL derives /healthz from the configured URL by replacing a trailing
// /ocr (or appending).
func healthURL(base string) string {
	b := strings.TrimRight(base, "/")
	if strings.HasSuffix(b, "/ocr") {
		b = strings.TrimSuffix(b, "/ocr")
	}
	return b + "/healthz"
}

// Probe checks GET /healthz.
func (d *Docling) Probe(ctx context.Context) (bool, string) {
	return d.b.probe(ctx, healthURL(d.b.baseURL))
}

// OCR posts files to an OCR service (rapidocr, tesseract, paddle).
type OCR struct {
	b    httpBase
	name string
}

// NewOCR returns an OCR engine; name is "rapidocr", "tesseract" or "paddle".
func NewOCR(h Doer, name, url, token string, timeout time.Duration) *OCR {
	return &OCR{name: name, b: httpBase{engine: "ocr", baseURL: strings.TrimRight(url, "/"), token: token, timeout: timeout, http: h, kind: outbound.KindTrusted}}
}

// Name implements Engine.
func (o *OCR) Name() string { return o.name }

// Extract posts file, name, MIME, optional page ranges, model and prompt.
func (o *OCR) Extract(ctx context.Context, r Request) (*Result, error) {
	ctx, cancel := o.b.ctx(ctx)
	defer cancel()
	req, err := o.b.multipart(ctx, o.b.baseURL, map[string]string{"name": r.Name, "mime_type": r.MIME, "pages": r.Pages, "model": r.Model, "prompt": r.Prompt}, "file", r)
	if err != nil {
		return nil, coded("ocr_invalid_file_path", err.Error())
	}
	status, body, _, err := o.b.do(ctx, req)
	if err != nil {
		return nil, err
	}
	if status/100 != 2 {
		return nil, o.b.statusError(status, body, false)
	}
	text, pages := parseFlexible(body)
	text = NormalizeText(text)
	if text == "" {
		return nil, coded("ocr_empty_content", "")
	}
	return &Result{Text: text, PageTexts: pages, Pages: len(pages), Engine: o.name}, nil
}

// Probe checks GET /healthz.
func (o *OCR) Probe(ctx context.Context) (bool, string) {
	return o.b.probe(ctx, healthURL(o.b.baseURL))
}

var _ = http.MethodGet
