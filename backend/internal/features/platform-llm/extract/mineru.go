package extract

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
)

// MinerU modes and defaults.
const (
	MinerUCloud      = "cloud"
	MinerUSelfHosted = "self_hosted"
	// DefaultMinerUBase is the cloud API base.
	DefaultMinerUBase = "https://mineru.net/api/v4"

	mineruZipLimit   = 100 << 20
	mineruEntryLimit = 20 << 20
)

// MinerU extracts documents with the MinerU service in either mode.
type MinerU struct {
	b        httpBase
	mode     string
	pollWait time.Duration
	sleep    func(ctx context.Context, d time.Duration) error
	maxPolls int
}

// NewMinerU returns the engine. Timeout defaults to 180 s. sleep may be nil.
func NewMinerU(h Doer, mode, base, token string, timeout time.Duration, sleep func(context.Context, time.Duration) error) *MinerU {
	if timeout <= 0 {
		timeout = 180 * time.Second
	}
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode != MinerUSelfHosted {
		mode = MinerUCloud
	}
	if strings.TrimSpace(base) == "" && mode == MinerUCloud {
		base = DefaultMinerUBase
	}
	kind := outbound.KindTrusted
	if mode == MinerUCloud {
		kind = outbound.KindStrict
	}
	if sleep == nil {
		sleep = func(ctx context.Context, d time.Duration) error {
			t := time.NewTimer(d)
			defer t.Stop()
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-t.C:
				return nil
			}
		}
	}
	return &MinerU{mode: mode, pollWait: 2 * time.Second, sleep: sleep, maxPolls: 1000,
		b: httpBase{engine: "mineru", baseURL: strings.TrimRight(base, "/"), token: token, timeout: timeout, http: h, kind: kind}}
}

// Name implements Engine.
func (m *MinerU) Name() string { return "mineru" }

// Extract runs the configured flow, falling back to the other one when the
// endpoint is not found (404).
func (m *MinerU) Extract(ctx context.Context, r Request) (*Result, error) {
	ctx, cancel := m.b.ctx(ctx)
	defer cancel()
	first, second := m.selfHosted, m.cloud
	if m.mode == MinerUCloud {
		first, second = m.cloud, m.selfHosted
	}
	res, err := first(ctx, r)
	if err != nil && IsCode(err, "mineru_http_404") {
		return second(ctx, r)
	}
	return res, err
}

func (m *MinerU) selfHosted(ctx context.Context, r Request) (*Result, error) {
	req, err := m.b.multipart(ctx, m.b.baseURL+"/file_parse", map[string]string{"return_md": "true"}, "files", r)
	if err != nil {
		return nil, coded("mineru_invalid_file_path", err.Error())
	}
	status, body, _, err := m.b.do(ctx, req)
	if err != nil {
		return nil, err
	}
	if status/100 != 2 {
		return nil, m.b.statusError(status, body, false)
	}
	var v any
	if json.Unmarshal(body, &v) != nil {
		return nil, coded("mineru_invalid_response", "")
	}
	text := mineruMarkdown(v)
	if text = NormalizeText(text); text == "" {
		return nil, coded("mineru_empty_content", "")
	}
	return &Result{Text: text, Engine: "mineru"}, nil
}

// mineruMarkdown finds md_content anywhere in the response.
func mineruMarkdown(v any) string {
	switch x := v.(type) {
	case map[string]any:
		for _, k := range []string{"md_content", "markdown", "text", "content"} {
			if s, ok := x[k].(string); ok && strings.TrimSpace(s) != "" {
				return s
			}
		}
		var parts []string
		for _, k := range []string{"results", "data", "result"} {
			if n, ok := x[k]; ok {
				if t := mineruMarkdown(n); t != "" {
					parts = append(parts, t)
				}
			}
		}
		if len(parts) == 0 {
			// results keyed by file name
			for _, e := range x {
				if t := mineruMarkdown(e); t != "" {
					parts = append(parts, t)
				}
			}
		}
		return strings.Join(parts, "\n")
	case []any:
		var parts []string
		for _, e := range x {
			if t := mineruMarkdown(e); t != "" {
				parts = append(parts, t)
			}
		}
		return strings.Join(parts, "\n")
	}
	return ""
}

func (m *MinerU) jsonReq(ctx context.Context, method, target string, body any) (*http.Request, error) {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, target, rd)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")
	if tok := strings.TrimSpace(m.b.token); tok != "" {
		req.Header.Set("Authorization", "Bearer "+tok)
	}
	return req, nil
}

func (m *MinerU) cloud(ctx context.Context, r Request) (*Result, error) {
	name := r.Name
	if name == "" {
		name = "document"
	}
	create, err := m.jsonReq(ctx, http.MethodPost, m.b.baseURL+"/file-urls/batch", map[string]any{
		"language": "ch", "enable_formula": true, "enable_table": true, "is_ocr": true,
		"files": []any{map[string]any{"name": name, "is_ocr": true}},
	})
	if err != nil {
		return nil, coded("mineru_unavailable", "invalid url")
	}
	status, body, _, err := m.b.do(ctx, create)
	if err != nil {
		return nil, err
	}
	if status/100 != 2 {
		return nil, m.b.statusError(status, body, false)
	}
	var created struct {
		Data struct {
			BatchID  string   `json:"batch_id"`
			FileURLs []string `json:"file_urls"`
		} `json:"data"`
	}
	if json.Unmarshal(body, &created) != nil || created.Data.BatchID == "" || len(created.Data.FileURLs) == 0 {
		return nil, coded("mineru_invalid_response", "")
	}
	put, err := http.NewRequestWithContext(ctx, http.MethodPut, created.Data.FileURLs[0], bytes.NewReader(r.Data))
	if err != nil {
		return nil, coded("mineru_invalid_response", "bad upload url")
	}
	pst, pbody, _, err := m.b.do(ctx, put)
	if err != nil {
		return nil, err
	}
	if pst/100 != 2 {
		return nil, m.b.statusError(pst, pbody, false)
	}
	for i := 0; i < m.maxPolls; i++ {
		if err := m.sleep(ctx, m.pollWait); err != nil {
			return nil, coded("mineru_unavailable", err.Error())
		}
		req, err := m.jsonReq(ctx, http.MethodGet, m.b.baseURL+"/extract-results/batch/"+created.Data.BatchID, nil)
		if err != nil {
			return nil, coded("mineru_unavailable", "invalid url")
		}
		st, sb, _, err := m.b.do(ctx, req)
		if err != nil {
			return nil, err
		}
		if st/100 != 2 {
			return nil, m.b.statusError(st, sb, false)
		}
		var res struct {
			Data struct {
				Results []struct {
					State  string `json:"state"`
					ZipURL string `json:"full_zip_url"`
					ErrMsg string `json:"err_msg"`
				} `json:"extract_result"`
			} `json:"data"`
		}
		if json.Unmarshal(sb, &res) != nil || len(res.Data.Results) == 0 {
			return nil, coded("mineru_invalid_response", "")
		}
		item := res.Data.Results[0]
		switch strings.ToLower(item.State) {
		case "done", "success", "completed":
			return m.fetchZip(ctx, item.ZipURL)
		case "failed", "error":
			if item.ErrMsg != "" {
				return nil, coded("mineru_unprocessable", item.ErrMsg)
			}
			return nil, coded("mineru_failed", "")
		}
	}
	return nil, coded("mineru_failed", "timed out waiting for the result")
}

func (m *MinerU) fetchZip(ctx context.Context, zipURL string) (*Result, error) {
	if zipURL == "" {
		return nil, coded("mineru_invalid_response", "missing result url")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, zipURL, nil)
	if err != nil {
		return nil, coded("mineru_invalid_response", "bad result url")
	}
	resp, err := m.b.http.Do(ctx, m.b.endpoint(), req)
	if err != nil {
		return nil, &Error{Code: "mineru_unavailable", Detail: "result download failed", Cause: err}
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		return nil, m.b.statusError(resp.StatusCode, nil, false)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, mineruZipLimit+1))
	if err != nil {
		return nil, &Error{Code: "mineru_unavailable", Detail: "result download failed", Cause: err}
	}
	if len(data) > mineruZipLimit {
		return nil, coded("mineru_invalid_response", "result archive is too large")
	}
	zr, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, coded("mineru_invalid_response", "result is not a zip archive")
	}
	pick := func(match func(string) bool) *zip.File {
		for _, f := range zr.File {
			if !f.FileInfo().IsDir() && match(strings.ToLower(f.Name)) {
				return f
			}
		}
		return nil
	}
	f := pick(func(n string) bool { return n == "full.md" || strings.HasSuffix(n, "/full.md") })
	if f == nil {
		f = pick(func(n string) bool { return strings.HasSuffix(n, ".md") || strings.HasSuffix(n, ".markdown") })
	}
	if f == nil {
		return nil, coded("mineru_empty_content", "")
	}
	rc, err := f.Open()
	if err != nil {
		return nil, coded("mineru_invalid_response", "unreadable archive entry")
	}
	defer rc.Close()
	md, err := io.ReadAll(io.LimitReader(rc, mineruEntryLimit+1))
	if err != nil || len(md) > mineruEntryLimit {
		return nil, coded("mineru_invalid_response", "markdown entry is too large")
	}
	text := NormalizeText(string(md))
	if text == "" {
		return nil, coded("mineru_empty_content", "")
	}
	return &Result{Text: text, Engine: "mineru"}, nil
}

// Probe checks the self-hosted OpenAPI/docs endpoints, or the cloud API base.
func (m *MinerU) Probe(ctx context.Context) (bool, string) {
	if m.mode == MinerUCloud {
		return m.b.probe(ctx, m.b.baseURL)
	}
	for _, p := range []string{"/openapi.json", "/docs"} {
		if ok, d := m.b.probe(ctx, m.b.baseURL+p); ok {
			return true, d
		}
	}
	return false, "no openapi or docs endpoint answered"
}

var _ = errors.New
var _ = fmt.Sprint
