package channel

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// ---- protocol adapters (model-provider clients) ---------------------------------

// UpstreamCall describes one outbound call to an upstream.
type UpstreamCall struct {
	Protocol       string
	BaseURL        string
	APIKey         string
	HeadersJSON    string // merged upstream + route headers
	ConnectTimeout time.Duration
	ReadTimeout    time.Duration
	AppName        string // attribution sent to the upstream
	WebBaseURL     string
}

// RemoteModel is one entry of an upstream's model list.
type RemoteModel struct {
	ID      string
	OwnedBy string
}

// ProbeRequest is the tiny request of a connectivity probe.
type ProbeRequest struct {
	UpstreamModel string
	Prompt        string
	MaxTokens     int
	Temperature   float64
	Task          string
}

// ProbeDebug is the exchange captured by an adapter.
type ProbeDebug struct {
	Request  ProbeDebugRequest  `json:"request"`
	Response ProbeDebugResponse `json:"response"`
}

// ProbeDebugRequest is the captured request.
type ProbeDebugRequest struct {
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body"`
}

// ProbeDebugResponse is the captured response.
type ProbeDebugResponse struct {
	StatusCode int               `json:"statusCode"`
	Headers    map[string]string `json:"headers,omitempty"`
	Body       string            `json:"body"`
}

// ProbeExchange is what an adapter reports for a probe that reached the
// upstream and got a 2xx answer.
type ProbeExchange struct {
	// Incompatible is set when the 2xx body could not be understood.
	Incompatible bool
	Debug        *ProbeDebug
}

// ModelClient is implemented by the protocol adapters (the wire formats are
// outside this area). All methods must honor ctx and the call's timeouts.
type ModelClient interface {
	// ListModels asks the upstream for its model list.
	ListModels(ctx context.Context, call UpstreamCall) ([]RemoteModel, error)
	// Probe sends one non-streaming request. A non-2xx answer must be returned
	// as an *UpstreamError (with the adapter's captured exchange attached).
	Probe(ctx context.Context, call UpstreamCall, req ProbeRequest) (*ProbeExchange, error)
}

// ErrUnsupportedAdapter is returned by an adapter that has no transport for the
// protocol.
var ErrUnsupportedAdapter = errors.New("channel: unsupported adapter")

// UpstreamError is a failed upstream call.
type UpstreamError struct {
	StatusCode int
	Header     http.Header
	Err        error
	// Accepted marks a failure after the upstream already accepted the request
	// (for example a stream that broke after the first byte); such failures
	// must not be retried elsewhere.
	Accepted bool
	// Debug is the captured exchange of a probe, when the adapter has one.
	Debug *ProbeDebug
}

func (e *UpstreamError) Error() string {
	if e.Err != nil {
		return e.Err.Error()
	}
	return "upstream request failed with status " + itoa(e.StatusCode)
}

// Unwrap exposes the cause.
func (e *UpstreamError) Unwrap() error { return e.Err }

// RequestAccepted reports whether the upstream had already accepted the request.
func (e *UpstreamError) RequestAccepted() bool { return e.Accepted }

type acceptedMarker interface{ RequestAccepted() bool }

// ---- other areas ---------------------------------------------------------------

// Pricing is the public price of a model, supplied by the billing area.
type Pricing struct {
	Currency                 string          `json:"currency"`
	IsFree                   bool            `json:"isFree"`
	Mode                     string          `json:"mode"`
	InputUSDPerMTokens       float64         `json:"inputUSDPerMTokens"`
	CacheReadUSDPerMTokens   float64         `json:"cacheReadUSDPerMTokens"`
	CacheWriteUSDPerMTokens  float64         `json:"cacheWriteUSDPerMTokens"`
	CacheWrite5mMultiplier   float64         `json:"cacheWrite5mMultiplier"`
	CacheWrite1hMultiplier   float64         `json:"cacheWrite1hMultiplier"`
	OutputUSDPerMTokens      float64         `json:"outputUSDPerMTokens"`
	CallUSDPerCall           float64         `json:"callUSDPerCall"`
	DurationUSDPerSecond     float64         `json:"durationUSDPerSecond"`
	Tiers                    []PricingTier   `json:"tiers"`
	SchedulePeriods          []PricingPeriod `json:"schedulePeriods"`
	ScheduleUTCOffsetMinutes int             `json:"scheduleUTCOffsetMinutes"`
}

// PricingTier is one token tier of a price.
type PricingTier struct {
	FromTokens              int64   `json:"fromTokens"`
	UpToTokens              *int64  `json:"upToTokens"`
	InputUSDPerMTokens      float64 `json:"inputUSDPerMTokens"`
	CacheReadUSDPerMTokens  float64 `json:"cacheReadUSDPerMTokens"`
	CacheWriteUSDPerMTokens float64 `json:"cacheWriteUSDPerMTokens"`
	OutputUSDPerMTokens     float64 `json:"outputUSDPerMTokens"`
}

// PricingPeriod is a scheduled rate period.
type PricingPeriod struct {
	Name        string `json:"name"`
	Weekdays    []int  `json:"weekdays"`
	Start       string `json:"start"`
	End         string `json:"end"`
	RatePercent int    `json:"ratePercent"`
}

// BillingModeSelf is the billing mode in which users bring their own cost
// and no price is required to list a model.
const BillingModeSelf = "self"

// PricingSource is implemented by the billing area.
type PricingSource interface {
	// BillingMode returns the current billing mode ("self" or other).
	BillingMode(ctx context.Context) string
	// PublicPrices returns the public price per exact platform model name. The
	// second result tells whether a price filter is configured at all.
	PublicPrices(ctx context.Context) (map[string]*Pricing, bool, error)
}

// PersonalModelSource supplies the models of a user's own provider keys.
type PersonalModelSource interface {
	PersonalModels(ctx context.Context, userID uint) ([]PublicModel, error)
}

// SubscriptionGroupSource resolves the model access group bound to the user's
// currently active subscription plan.
type SubscriptionGroupSource interface {
	SubscriptionGroupID(ctx context.Context, userID uint, now time.Time) (groupID uint, ok bool, err error)
}

// SubscriptionUserLister may be implemented by the SubscriptionGroupSource to
// enumerate the users whose currently active plan is bound to a group. It feeds
// the subscription figures of the group listings; without it they are zero.
type SubscriptionUserLister interface {
	SubscriptionGroupUserIDs(ctx context.Context, groupID uint, now time.Time) ([]uint, error)
}

// IconUsageCounter counts references to an icon that live in other areas
// (the conversation run snapshot).
type IconUsageCounter interface {
	CountIconReferences(ctx context.Context, ref string) (int64, error)
}

// UserCounter counts users for the group listings.
type UserCounter interface {
	CountAllUsers(ctx context.Context) (int64, error)
}

// CatalogFetcher downloads the models.dev document.
type CatalogFetcher interface {
	Fetch(ctx context.Context) ([]byte, error)
}

// ---- icon storage ----------------------------------------------------------------

// ErrObjectNotFound is returned by an IconStore for a missing object.
var ErrObjectNotFound = errors.New("channel: object not found")

// IconStore is the object store holding uploaded icons.
type IconStore interface {
	Put(ctx context.Context, path string, contentType string, data []byte) error
	Get(ctx context.Context, path string) ([]byte, error)
	Delete(ctx context.Context, path string) error
}

// dirIconStore keeps objects below a root directory.
type dirIconStore struct{ root string }

func newDirIconStore(root string) *dirIconStore { return &dirIconStore{root: root} }

func (d *dirIconStore) resolve(path string) (string, error) {
	clean := filepath.Clean("/" + strings.TrimPrefix(path, "/"))
	return filepath.Join(d.root, clean), nil
}

func (d *dirIconStore) Put(_ context.Context, path, _ string, data []byte) error {
	full, _ := d.resolve(path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(full), ".upload-*")
	if err != nil {
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		os.Remove(tmp.Name())
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmp.Name())
		return err
	}
	return os.Rename(tmp.Name(), full)
}

func (d *dirIconStore) Get(_ context.Context, path string) ([]byte, error) {
	full, _ := d.resolve(path)
	b, err := os.ReadFile(full)
	if errors.Is(err, os.ErrNotExist) {
		return nil, ErrObjectNotFound
	}
	return b, err
}

func (d *dirIconStore) Delete(_ context.Context, path string) error {
	full, _ := d.resolve(path)
	err := os.Remove(full)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return nil
}

// httpCatalogFetcher downloads https://models.dev/api.json.
type httpCatalogFetcher struct {
	url    string
	client *http.Client
}

func newHTTPCatalogFetcher() *httpCatalogFetcher {
	return &httpCatalogFetcher{url: "https://models.dev/api.json", client: &http.Client{Timeout: 30 * time.Second}}
}

func (f *httpCatalogFetcher) Fetch(ctx context.Context) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, f.url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/json")
	resp, err := f.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, errors.New("models.dev answered status " + itoa(resp.StatusCode))
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxCatalogAPIBytes+1))
	if err != nil {
		return nil, err
	}
	if len(body) > maxCatalogAPIBytes {
		return nil, errors.New("models.dev response too large")
	}
	return body, nil
}

// ---- fail-over advice ---------------------------------------------------------------

// ShouldFailOver is the pure fail-over advice for an upstream failure: retry on
// another route for timeouts, throttling, server errors and broken connections,
// never for caller cancellation, requests the upstream already accepted, or
// client-side (4xx) mistakes.
func ShouldFailOver(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) {
		return false
	}
	var acc acceptedMarker
	if errors.As(err, &acc) && acc.RequestAccepted() {
		return false
	}
	var ue *UpstreamError
	if errors.As(err, &ue) && ue.StatusCode > 0 {
		return ue.StatusCode == http.StatusRequestTimeout || ue.StatusCode == http.StatusTooManyRequests || ue.StatusCode >= 500
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, io.EOF) || errors.Is(err, io.ErrUnexpectedEOF) {
		return true
	}
	var ne net.Error
	return errors.As(err, &ne)
}
