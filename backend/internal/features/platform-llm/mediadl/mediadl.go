// Package mediadl downloads temporary media (images, videos) that providers
// return as URLs. Signed URLs never appear in errors or traces.
package mediadl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"github.com/riochat/riochat/backend/internal/features/platform-llm/outbound"
	"github.com/riochat/riochat/backend/internal/features/platform-llm/trace"
)

// Kind is the expected media type.
type Kind int

// Media kinds.
const (
	Image Kind = iota
	Video
)

// Timeouts and limits from the platform contract.
const (
	ImageTimeout       = 60 * time.Second
	VideoTimeout       = 120 * time.Second
	FileStatusTimeout  = 30 * time.Second
	FileStatusMaxBytes = 1 << 20
	ErrorDrainBytes    = 64 << 10
	GeminiFilePolls    = 12
	GeminiFilePollWait = 5 * time.Second
)

// Errors (matched with errors.Is).
var (
	ErrTooLarge       = errors.New("media exceeds the size limit")
	ErrNotReady       = errors.New("media file is not ready")
	ErrUnsupported    = errors.New("media source is not supported")
	ErrInvalidRequest = errors.New("invalid media download request")
	ErrHTTPStatus     = errors.New("media download failed")
)

// Doer executes requests through the outbound pool.
type Doer interface {
	Do(ctx context.Context, ep outbound.Endpoint, req *http.Request) (*http.Response, error)
}

// Downloader fetches media.
type Downloader struct {
	HTTP   Doer
	Sleep  func(ctx context.Context, d time.Duration) error
	Tracer *trace.Tracer
}

// Request describes one download.
type Request struct {
	URL      string
	Kind     Kind
	MaxBytes int64 // must be positive
	// EndpointURL is the model endpoint the artifact came from. Only a URL on
	// the same origin inherits that endpoint's trust and API key.
	EndpointURL string
	APIKey      string
	// Untrusted marks a user-supplied endpoint (public-only, never relaxed).
	Untrusted bool
	// GoogleKey sends the key as x-goog-api-key instead of a bearer token.
	GoogleKey bool
}

// Media is a downloaded artifact.
type Media struct {
	Data []byte
	// MIME is the response Content-Type main type when it has the expected
	// image/ or video/ prefix, else empty (the caller sniffs).
	MIME string
}

func sleepCtx(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// scrub removes the URL from transport errors.
func scrub(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) && ue.Err != nil {
		return fmt.Errorf("media download failed: %w", ue.Err)
	}
	return fmt.Errorf("media download failed: %w", err)
}

var geminiFileRe = regexp.MustCompile(`^(.*?/files/[^/:?#]+)`)

func isGeminiFile(u *url.URL) bool {
	return strings.EqualFold(u.Hostname(), "generativelanguage.googleapis.com") && geminiFileRe.MatchString(u.Path)
}

// Download fetches the artifact.
func (d *Downloader) Download(ctx context.Context, r Request) (*Media, error) {
	if r.MaxBytes <= 0 {
		return nil, fmt.Errorf("%w: the size cap must be positive", ErrInvalidRequest)
	}
	u, err := url.Parse(strings.TrimSpace(r.URL))
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("%w: invalid url", ErrInvalidRequest)
	}
	timeout := ImageTimeout
	if r.Kind == Video {
		timeout = VideoTimeout
	}
	ctx, cancel := context.WithTimeout(trace.WithoutURL(ctx), timeout)
	defer cancel()

	if isGeminiFile(u) {
		return d.geminiFile(ctx, r, u)
	}
	return d.fetch(ctx, r, u.String(), r.APIKey != "" && sameOriginKey(r), nil)
}

func sameOriginKey(r Request) bool {
	return r.EndpointURL != "" && outbound.SameOrigin(r.EndpointURL, r.URL)
}

func (d *Downloader) endpoint(r Request, target string) outbound.Endpoint {
	if r.EndpointURL != "" && outbound.SameOrigin(r.EndpointURL, target) {
		ep := outbound.Endpoint{URL: r.EndpointURL, Kind: outbound.KindTrusted, Redirects: outbound.RedirectSameOrigin, Integration: "media download"}
		if r.Untrusted {
			ep.Kind, ep.Redirects = outbound.KindUserSupplied, outbound.RedirectNone
		}
		return ep
	}
	return outbound.Endpoint{Kind: outbound.KindStrict, Integration: "media download"}
}

func (d *Downloader) sleep(ctx context.Context, dur time.Duration) error {
	if d.Sleep != nil {
		return d.Sleep(ctx, dur)
	}
	return sleepCtx(ctx, dur)
}

// fetch performs the GET. withKey sends the endpoint's key; extra headers are
// added as given (used for the Gemini key header).
func (d *Downloader) fetch(ctx context.Context, r Request, target string, withKey bool, extra http.Header) (*Media, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid url", ErrInvalidRequest)
	}
	if withKey {
		if r.GoogleKey {
			req.Header.Set("x-goog-api-key", r.APIKey)
		} else {
			req.Header.Set("Authorization", "Bearer "+r.APIKey)
		}
	}
	for k, v := range extra {
		req.Header[k] = v
	}
	resp, err := d.HTTP.Do(ctx, d.endpoint(r, target), req)
	if err != nil {
		return nil, scrub(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, ErrorDrainBytes))
		what := "image"
		if r.Kind == Video {
			what = "video"
		}
		return nil, fmt.Errorf("%w: %s download returned HTTP status %d", ErrHTTPStatus, what, resp.StatusCode)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, r.MaxBytes+1))
	if err != nil {
		return nil, scrub(err)
	}
	if int64(len(data)) > r.MaxBytes {
		return nil, fmt.Errorf("%w: limit is %d bytes", ErrTooLarge, r.MaxBytes)
	}
	return &Media{Data: data, MIME: mainType(resp.Header.Get("Content-Type"), r.Kind)}, nil
}

func mainType(ct string, k Kind) string {
	ct = strings.ToLower(strings.TrimSpace(ct))
	if i := strings.Index(ct, ";"); i >= 0 {
		ct = strings.TrimSpace(ct[:i])
	}
	prefix := "image/"
	if k == Video {
		prefix = "video/"
	}
	if strings.HasPrefix(ct, prefix) {
		return ct
	}
	return ""
}

type fileMeta struct {
	State    string `json:"state"`
	MimeType string `json:"mimeType"`
}

// geminiFile downloads from the Gemini Files API: videos only, with a key,
// after the file reports ACTIVE.
func (d *Downloader) geminiFile(ctx context.Context, r Request, u *url.URL) (*Media, error) {
	if r.Kind != Video {
		return nil, fmt.Errorf("%w: Gemini file URIs are supported for videos only", ErrUnsupported)
	}
	if strings.TrimSpace(r.APIKey) == "" {
		return nil, fmt.Errorf("%w: downloading a Gemini file requires an API key", ErrInvalidRequest)
	}
	base := geminiFileRe.FindStringSubmatch(u.Path)[1]
	metaURL := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: base}).String()
	keyHdr := http.Header{"X-Goog-Api-Key": []string{r.APIKey}}

	var meta fileMeta
	for attempt := 1; attempt <= GeminiFilePolls; attempt++ {
		m, err := d.fileStatus(ctx, r, metaURL, keyHdr)
		if err != nil {
			return nil, err
		}
		meta = m
		state := strings.ToUpper(m.State)
		if state == "ACTIVE" {
			break
		}
		if state == "FAILED" {
			return nil, fmt.Errorf("%w: the file failed to process", ErrNotReady)
		}
		if attempt == GeminiFilePolls {
			return nil, fmt.Errorf("%w: last state %q after %d checks", ErrNotReady, meta.State, GeminiFilePolls)
		}
		if err := d.sleep(ctx, GeminiFilePollWait); err != nil {
			return nil, err
		}
	}
	dl := (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: base + ":download", RawQuery: "alt=media"}).String()
	// The file service key goes in its header only; no bearer token.
	nr := r
	nr.EndpointURL = ""
	media, err := d.fetch(ctx, nr, dl, false, keyHdr)
	if err != nil {
		return nil, err
	}
	if media.MIME == "" && strings.HasPrefix(strings.ToLower(meta.MimeType), "video/") {
		media.MIME = strings.ToLower(meta.MimeType)
	}
	return media, nil
}

func (d *Downloader) fileStatus(ctx context.Context, r Request, metaURL string, hdr http.Header) (fileMeta, error) {
	ctx, cancel := context.WithTimeout(ctx, FileStatusTimeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, metaURL, nil)
	if err != nil {
		return fileMeta{}, fmt.Errorf("%w: invalid url", ErrInvalidRequest)
	}
	for k, v := range hdr {
		req.Header[k] = v
	}
	resp, err := d.HTTP.Do(ctx, outbound.Endpoint{Kind: outbound.KindStrict, Integration: "media download"}, req)
	if err != nil {
		return fileMeta{}, scrub(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, ErrorDrainBytes))
		return fileMeta{}, fmt.Errorf("%w: file status returned HTTP status %d", ErrHTTPStatus, resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, FileStatusMaxBytes+1))
	if err != nil {
		return fileMeta{}, scrub(err)
	}
	if len(b) > FileStatusMaxBytes {
		return fileMeta{}, fmt.Errorf("%w: file status is too large", ErrTooLarge)
	}
	var m fileMeta
	if err := json.Unmarshal(b, &m); err != nil {
		return fileMeta{}, fmt.Errorf("%w: unreadable file status", ErrNotReady)
	}
	return m, nil
}
