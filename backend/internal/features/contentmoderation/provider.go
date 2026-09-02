package contentmoderation

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// Error codes recorded on failed-open events.
const (
	CodeTimeout         = "timeout"
	CodeRateLimited     = "rate_limited"
	CodeInvalidResponse = "invalid_response"
	CodeNetworkError    = "network_error"
	CodeServiceError    = "service_error"
	CodeQueueFull       = "queue_full"
	CodeWorkerLost      = "worker_lost"
	CodeConfigMissing   = "config_missing"
)

// publicErrorText is the fixed, safe description of each failure class. It is
// the only provider-derived text that is stored or shown.
var publicErrorText = map[string]string{
	CodeTimeout:         "content moderation timed out",
	CodeRateLimited:     "content moderation rate limited",
	CodeInvalidResponse: "content moderation invalid response",
	CodeNetworkError:    "content moderation network error",
	CodeServiceError:    "content moderation service error",
	CodeQueueFull:       "content moderation queue full",
	CodeWorkerLost:      "content moderation worker lost",
	CodeConfigMissing:   "content moderation configuration unavailable",
}

func publicTextFor(code string) string {
	if t, ok := publicErrorText[code]; ok {
		return t
	}
	return publicErrorText[CodeServiceError]
}

// ProviderError is a failure of the outbound moderation call. Its text is the
// fixed public description of the class; response bodies and credentials never
// appear in it.
type ProviderError struct {
	Code   string
	Status int
	cause  error
}

func (e *ProviderError) Error() string { return publicTextFor(e.Code) }

// Unwrap exposes the internal cause (kept for logs, never shown).
func (e *ProviderError) Unwrap() error { return e.cause }

// errorCodeOf maps any error to a failed-open error code.
func errorCodeOf(err error) string {
	var pe *ProviderError
	switch {
	case errors.As(err, &pe):
		return pe.Code
	case errors.Is(err, context.DeadlineExceeded):
		return CodeTimeout
	case errors.Is(err, context.Canceled):
		return CodeWorkerLost
	}
	return CodeServiceError
}

// ClientConfig is what the provider needs for one check.
type ClientConfig struct {
	BaseURL string
	APIKey  string
	Model   string
	Timeout time.Duration
}

// Scope describes what a check is looking for.
type Scope struct {
	Direction string
	Modality  string
	// Selected are the enforced categories. Empty disables early stopping (used
	// by probes).
	Selected []string
}

// ModerationResult is one result item of the provider response.
type ModerationResult struct {
	Flagged           bool
	Categories        map[string]bool
	CategoryScores    map[string]float64
	AppliedInputTypes map[string][]string
}

// Response is the merged answer to one check.
type Response struct {
	ID      string
	Model   string
	Results []ModerationResult
}

// Image is one picture sent for moderation.
type Image struct {
	Data []byte
	MIME string
}

// Provider performs moderation calls. The production implementation talks
// HTTP; tests and embedders may substitute their own.
type Provider interface {
	CheckText(ctx context.Context, cc ClientConfig, text string, scope Scope) (*Response, error)
	CheckImages(ctx context.Context, cc ClientConfig, images []Image, scope Scope) (*Response, error)
}

// Doer sends an HTTP request. The default is an SSRF-guarded client.
type Doer interface {
	Do(req *http.Request) (*http.Response, error)
}

const (
	maxResponseBytes = 4 << 20
	maxRetryWait     = 30 * time.Second
	defaultTimeout   = 10 * time.Second
	defaultModel     = "omni-moderation-latest"
)

// httpProvider implements Provider over an OpenAI-compatible endpoint.
type httpProvider struct {
	doer  Doer
	sleep func(ctx context.Context, d time.Duration) error
	// now is used to interpret an HTTP-date Retry-After.
	now func() time.Time
}

func newHTTPProvider(doer Doer, now func() time.Time) *httpProvider {
	return &httpProvider{doer: doer, sleep: sleepContext, now: now}
}

func sleepContext(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

func (p *httpProvider) CheckText(ctx context.Context, cc ClientConfig, text string, scope Scope) (*Response, error) {
	chunks := splitText(text, maxTextChunkBytes)
	if len(chunks) == 0 {
		return &Response{}, nil
	}
	ctx, cancel := withCheckDeadline(ctx, cc)
	defer cancel()
	merged := &Response{}
	for _, chunk := range chunks {
		input := []any{map[string]any{"type": "text", "text": chunk}}
		resp, err := p.call(ctx, cc, input)
		if err != nil {
			return nil, err
		}
		mergeResponse(merged, resp)
		if len(scope.Selected) > 0 && decideHit(resp, scope.Selected, scope.Modality).Hit {
			break
		}
	}
	return merged, nil
}

func (p *httpProvider) CheckImages(ctx context.Context, cc ClientConfig, images []Image, scope Scope) (*Response, error) {
	merged := &Response{}
	var batch []any
	size := 0
	var urls []any
	for _, img := range images {
		if len(img.Data) == 0 {
			continue
		}
		urls = append(urls, dataURLItem(img))
	}
	if len(urls) == 0 {
		return merged, nil
	}
	ctx, cancel := withCheckDeadline(ctx, cc)
	defer cancel()
	flush := func() (bool, error) {
		if len(batch) == 0 {
			return false, nil
		}
		resp, err := p.call(ctx, cc, batch)
		batch, size = nil, 0
		if err != nil {
			return false, err
		}
		mergeResponse(merged, resp)
		return len(scope.Selected) > 0 && decideHit(resp, scope.Selected, scope.Modality).Hit, nil
	}
	for _, item := range urls {
		l := itemURLLen(item)
		if len(batch) > 0 && size+l > maxImageBatchBytes {
			hit, err := flush()
			if err != nil {
				return nil, err
			}
			if hit {
				return merged, nil
			}
		}
		batch = append(batch, item)
		size += l
	}
	if _, err := flush(); err != nil {
		return nil, err
	}
	return merged, nil
}

func dataURLItem(img Image) map[string]any {
	mime := strings.TrimSpace(img.MIME)
	if mime == "" {
		mime = "image/png"
	}
	url := "data:" + mime + ";base64," + base64.StdEncoding.EncodeToString(img.Data)
	return map[string]any{"type": "image_url", "image_url": map[string]any{"url": url}}
}

func itemURLLen(item any) int {
	m := item.(map[string]any)
	return len(m["image_url"].(map[string]any)["url"].(string))
}

func withCheckDeadline(ctx context.Context, cc ClientConfig) (context.Context, context.CancelFunc) {
	d := cc.Timeout
	if d <= 0 {
		d = defaultTimeout
	}
	return context.WithTimeout(ctx, d)
}

func mergeResponse(dst, src *Response) {
	if src == nil {
		return
	}
	dst.Results = append(dst.Results, src.Results...)
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.ID != "" {
		dst.ID = src.ID
	}
}

type wireResult struct {
	Flagged                   bool                `json:"flagged"`
	Categories                map[string]bool     `json:"categories"`
	CategoryScores            map[string]float64  `json:"category_scores"`
	CategoryAppliedInputTypes map[string][]string `json:"category_applied_input_types"`
}

type wireResponse struct {
	ID      string       `json:"id"`
	Model   string       `json:"model"`
	Results []wireResult `json:"results"`
}

// call performs one logical request: at most two attempts sharing ctx's
// deadline.
func (p *httpProvider) call(ctx context.Context, cc ClientConfig, input []any) (*Response, error) {
	endpoint, err := ModerationEndpoint(cc.BaseURL)
	if err != nil {
		return nil, &ProviderError{Code: CodeServiceError, cause: err}
	}
	model := strings.TrimSpace(cc.Model)
	if model == "" {
		model = defaultModel
	}
	body, err := json.Marshal(map[string]any{"model": model, "input": input})
	if err != nil {
		return nil, &ProviderError{Code: CodeServiceError, cause: err}
	}
	var lastErr error
	for attempt := 0; attempt < 2; attempt++ {
		resp, wait, retry, err := p.attempt(ctx, endpoint, cc.APIKey, body)
		if err == nil {
			return resp, nil
		}
		lastErr = err
		if !retry || attempt == 1 {
			break
		}
		if serr := p.sleep(ctx, wait); serr != nil {
			return nil, deadlineOrCancel(ctx, serr)
		}
	}
	return nil, lastErr
}

// deadlineOrCancel turns a context failure into the right error: our own
// deadline is a timeout, a caller cancellation passes through.
func deadlineOrCancel(ctx context.Context, err error) error {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return &ProviderError{Code: CodeTimeout, cause: err}
	}
	return err
}

// attempt sends one HTTP request. It reports whether a retry is allowed and
// how long to wait first.
func (p *httpProvider) attempt(ctx context.Context, endpoint, apiKey string, body []byte) (resp *Response, wait time.Duration, retry bool, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, 0, false, &ProviderError{Code: CodeServiceError, cause: err}
	}
	req.Header.Set("Content-Type", "application/json")
	if key := strings.TrimSpace(apiKey); key != "" {
		req.Header.Set("Authorization", "Bearer "+key)
	}
	httpResp, err := p.doer.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, false, deadlineOrCancel(ctx, ctx.Err())
		}
		if isTimeoutErr(err) {
			return nil, 0, true, &ProviderError{Code: CodeTimeout, cause: err}
		}
		return nil, 0, transientNetworkErr(err), &ProviderError{Code: CodeNetworkError, cause: err}
	}
	defer httpResp.Body.Close()
	raw, readErr := io.ReadAll(io.LimitReader(httpResp.Body, maxResponseBytes))
	status := httpResp.StatusCode
	switch {
	case status == http.StatusTooManyRequests:
		return nil, p.retryAfter(httpResp.Header.Get("Retry-After")), true, &ProviderError{Code: CodeRateLimited, Status: status}
	case status < 200 || status > 299:
		retryable := status >= 500
		var w time.Duration
		if retryable {
			w = p.retryAfter(httpResp.Header.Get("Retry-After"))
		}
		return nil, w, retryable, &ProviderError{Code: CodeServiceError, Status: status,
			cause: fmt.Errorf("moderation service returned status %d", status)}
	}
	if readErr != nil {
		if ctx.Err() != nil {
			return nil, 0, false, deadlineOrCancel(ctx, ctx.Err())
		}
		return nil, 0, transientNetworkErr(readErr), &ProviderError{Code: CodeNetworkError, cause: readErr}
	}
	parsed, perr := parseResponse(raw)
	if perr != nil {
		return nil, 0, false, &ProviderError{Code: CodeInvalidResponse, cause: perr}
	}
	return parsed, 0, false, nil
}

func parseResponse(raw []byte) (*Response, error) {
	var w wireResponse
	if err := json.Unmarshal(raw, &w); err != nil {
		return nil, err
	}
	if len(w.Results) == 0 {
		return nil, errors.New("moderation response has no results")
	}
	out := &Response{ID: w.ID, Model: w.Model}
	for _, r := range w.Results {
		if r.Categories == nil {
			return nil, errors.New("moderation result lacks categories")
		}
		out.Results = append(out.Results, ModerationResult{
			Flagged: r.Flagged, Categories: r.Categories,
			CategoryScores: r.CategoryScores, AppliedInputTypes: r.CategoryAppliedInputTypes,
		})
	}
	return out, nil
}

func (p *httpProvider) retryAfter(h string) time.Duration {
	h = strings.TrimSpace(h)
	if h == "" {
		return 0
	}
	var d time.Duration
	if secs, err := strconv.Atoi(h); err == nil {
		d = time.Duration(secs) * time.Second
	} else if t, err := http.ParseTime(h); err == nil {
		d = t.Sub(p.now())
	}
	if d < 0 {
		return 0
	}
	if d > maxRetryWait {
		return maxRetryWait
	}
	return d
}

func isTimeoutErr(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// transientNetworkErr reports whether a transport failure merits a retry:
// connection reset or refused, unexpected EOF, or a timeout.
func transientNetworkErr(err error) bool {
	switch {
	case errors.Is(err, syscall.ECONNRESET), errors.Is(err, syscall.ECONNREFUSED),
		errors.Is(err, io.ErrUnexpectedEOF), errors.Is(err, io.EOF):
		return true
	}
	return isTimeoutErr(err)
}

// hitDecision is the outcome of evaluating a response against a policy scope.
type hitDecision struct {
	Hit        bool
	Categories []string
	Scores     map[string]float64
	// FirstResult is the index of the first result item carrying a hit, or -1.
	FirstResult int
}

// decideHit applies the hit rule: a category counts only when it is selected
// for the scope, its boolean is true and it applies to the scope's modality.
// The provider's overall "flagged" value is ignored.
func decideHit(resp *Response, selected []string, modality string) hitDecision {
	dec := hitDecision{FirstResult: -1, Scores: map[string]float64{}}
	if resp == nil || len(selected) == 0 {
		return dec
	}
	seen := map[string]bool{}
	for i, r := range resp.Results {
		for _, cat := range selected {
			if !r.Categories[cat] || !appliesTo(r.AppliedInputTypes[cat], modality) {
				continue
			}
			if dec.FirstResult < 0 {
				dec.FirstResult = i
			}
			if seen[cat] {
				continue
			}
			seen[cat] = true
			dec.Categories = append(dec.Categories, cat)
			dec.Scores[cat] = r.CategoryScores[cat]
		}
	}
	sort.Strings(dec.Categories)
	dec.Hit = len(dec.Categories) > 0
	return dec
}

func appliesTo(types []string, modality string) bool {
	if len(types) == 0 {
		return true
	}
	for _, t := range types {
		if strings.EqualFold(strings.TrimSpace(t), modality) {
			return true
		}
	}
	return false
}
