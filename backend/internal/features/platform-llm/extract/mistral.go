package extract

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

// DefaultMistralURL and DefaultMistralModel are the documented defaults.
const (
	DefaultMistralURL   = "https://api.mistral.ai/v1/ocr"
	DefaultMistralModel = "mistral-ocr-latest"
)

// MistralOCR sends documents inline to the Mistral OCR API.
type MistralOCR struct {
	b     httpBase
	model string
}

// NewMistralOCR returns the engine; empty url/model use the defaults.
func NewMistralOCR(h Doer, url, token, model string, timeout time.Duration) *MistralOCR {
	if strings.TrimSpace(url) == "" {
		url = DefaultMistralURL
	}
	if strings.TrimSpace(model) == "" {
		model = DefaultMistralModel
	}
	return &MistralOCR{model: model, b: httpBase{engine: "ocr", baseURL: strings.TrimRight(url, "/"), token: token, timeout: timeout, http: h, kind: outbound.KindTrusted}}
}

// Name implements Engine.
func (m *MistralOCR) Name() string { return "mistral" }

// parsePages turns "1,3-5" into 0-based indexes.
func parsePages(spec string) []int {
	var out []int
	seen := map[int]bool{}
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		lo, hi := part, part
		if i := strings.Index(part, "-"); i > 0 {
			lo, hi = part[:i], part[i+1:]
		}
		a, err1 := strconv.Atoi(strings.TrimSpace(lo))
		b, err2 := strconv.Atoi(strings.TrimSpace(hi))
		if err1 != nil || err2 != nil || a < 1 || b < a || b-a > 10000 {
			continue
		}
		for p := a; p <= b; p++ {
			if !seen[p-1] {
				seen[p-1] = true
				out = append(out, p-1)
			}
		}
	}
	sort.Ints(out)
	return out
}

// Extract posts the document as a data URL; PDFs use document_url with 0-based
// pages, images use image_url.
func (m *MistralOCR) Extract(ctx context.Context, r Request) (*Result, error) {
	ctx, cancel := m.b.ctx(ctx)
	defer cancel()
	mime := strings.ToLower(strings.TrimSpace(r.MIME))
	dataURL := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(r.Data)
	body := map[string]any{"model": m.model, "include_image_base64": false, "include_blocks": false}
	if mime == "application/pdf" || strings.HasSuffix(strings.ToLower(r.Name), ".pdf") {
		dataURL = "data:application/pdf;base64," + base64.StdEncoding.EncodeToString(r.Data)
		body["document"] = map[string]any{"type": "document_url", "document_url": dataURL}
		if pages := parsePages(r.Pages); len(pages) > 0 {
			body["pages"] = pages
		}
	} else {
		body["document"] = map[string]any{"type": "image_url", "image_url": dataURL}
	}
	payload, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, m.b.baseURL, bytes.NewReader(payload))
	if err != nil {
		return nil, coded("ocr_unavailable", "invalid url")
	}
	req.Header.Set("Content-Type", "application/json")
	if tok := strings.TrimSpace(m.b.token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	status, resp, _, err := m.b.do(ctx, req)
	if err != nil {
		return nil, err
	}
	if status/100 != 2 {
		return nil, m.b.statusError(status, resp, false)
	}
	var doc struct {
		Pages []struct {
			Index    int    `json:"index"`
			Markdown string `json:"markdown"`
		} `json:"pages"`
	}
	if json.Unmarshal(resp, &doc) != nil {
		return nil, coded("ocr_unprocessable", "invalid response")
	}
	sort.SliceStable(doc.Pages, func(i, j int) bool { return doc.Pages[i].Index < doc.Pages[j].Index })
	var texts []string
	for _, p := range doc.Pages {
		texts = append(texts, p.Markdown)
	}
	text := NormalizeText(strings.Join(texts, "\n"))
	if text == "" {
		return nil, coded("ocr_empty_content", "")
	}
	return &Result{Text: text, Pages: len(doc.Pages), PageTexts: texts, Engine: "mistral"}, nil
}

// Probe reports whether the service answers; the OCR URL itself only accepts
// POST, so any non-5xx answer to a GET means reachable.
func (m *MistralOCR) Probe(ctx context.Context) (bool, string) { return m.b.probe(ctx, m.b.baseURL) }
