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
