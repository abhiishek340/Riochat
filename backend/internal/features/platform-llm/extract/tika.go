package extract

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

// Tika extracts text with an Apache Tika server.
type Tika struct{ b httpBase }

// NewTika returns a Tika engine for base URL url.
func NewTika(h Doer, url, token string, timeout time.Duration) *Tika {
	return &Tika{b: httpBase{engine: "tika", baseURL: strings.TrimRight(url, "/"), token: token, timeout: timeout, http: h, kind: outbound.KindTrusted}}
}

// Name implements Engine.
func (t *Tika) Name() string { return "tika" }

// Extract streams the file to PUT {base}/tika asking for plain text.
func (t *Tika) Extract(ctx context.Context, r Request) (*Result, error) {
	ctx, cancel := t.b.ctx(ctx)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, t.b.baseURL+"/tika", bytes.NewReader(r.Data))
	if err != nil {
		return nil, coded("tika_unavailable", "invalid url")
	}
	req.Header.Set("Accept", "text/plain")
	if r.MIME != "" {
		req.Header.Set("Content-Type", r.MIME)
	}
	if r.Name != "" {
		req.Header.Set("X-Tika-Filename", r.Name)
	}
	if tok := strings.TrimSpace(t.b.token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	status, body, _, err := t.b.do(ctx, req)
	if err != nil {
		return nil, err
	}
	if status == http.StatusNoContent {
		return nil, coded("tika_empty_content", "")
	}
	if status/100 != 2 {
		return nil, t.b.statusError(status, body, true)
	}
	text := NormalizeText(string(body))
	if text == "" {
		return nil, coded("tika_empty_content", "")
	}
	return &Result{Text: text, Engine: "tika"}, nil
}

// Probe checks GET {base}/tika.
func (t *Tika) Probe(ctx context.Context) (bool, string) { return t.b.probe(ctx, t.b.baseURL+"/tika") }
