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
