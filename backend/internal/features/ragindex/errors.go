package ragindex

import (
	"context"
	"errors"
	"net/http"

	"github.com/riochat/riochat/backend/internal/platform/logx"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Public errors (codes are API contracts shared with the settings and
// conversation areas, which register the same texts).
var (
	ErrTooManyFiles  = apperr.Define("embedding.too_many_files", "too many files for embedding").WithStatus(http.StatusBadRequest)
	ErrNotConfigured = apperr.Define("embedding.service_not_configured", "embedding service is not configured").WithStatus(http.StatusServiceUnavailable)
	ErrUnavailable   = apperr.Define("embedding.service_unavailable", "embedding service is not available").WithStatus(http.StatusServiceUnavailable)
	ErrSubmitFailed  = apperr.Define("embedding.submit_failed", "failed to submit embedding jobs").WithStatus(http.StatusInternalServerError)
	// ErrReindexRunning is returned (with the outcome) when a bulk rebuild is
	// already running in this process.
	ErrReindexRunning = errors.New("ragindex: a rebuild is already running")
)

// Internal sentinels.
var (
	// ErrModalityUnsupported is returned by an Embedder that cannot embed the
	// given input type (an image sent to a text-only protocol).
	ErrModalityUnsupported = errors.New("ragindex: protocol cannot embed this input type")
	// ErrProviderUnavailable wraps provider failures that mean "the embedding
	// service did not answer properly" (transport errors, non-200 answers).
	ErrProviderUnavailable = errors.New("ragindex: embedding provider unavailable")
	// ErrConfigChanged marks a run that found the configuration changed.
	errConfigChanged = errors.New("ragindex: configuration changed")
	errNoContent     = errors.New("ragindex: no usable content")
	errImageTooLarge = errors.New("ragindex: image too large after resize")
	errImageFormat   = errors.New("ragindex: image format not supported")
	errNotConfigured = errors.New("ragindex: embedding service not configured")
	errBatchMismatch = errors.New("ragindex: embedding batch size mismatch")
	errQueue         = errors.New("ragindex: queue unavailable")
)

// User-visible summaries stored in embed_error and returned to clients. They
// never carry provider text, URLs, keys or storage details.
const (
	errCanceled        = "indexing canceled"
	errTimedOut        = "indexing timed out"
	errServiceConf     = "embedding service is not configured"
	errServiceDownMsg  = "embedding service unavailable"
	errNoText          = "no extractable text"
	errNoUsable        = "no usable content"
	errConfigResubmit  = "configuration changed, resubmit"
	errConfigDuring    = "configuration changed during processing"
	errReindexRequired = "configuration changed, reindex required"
	errImageBig        = "image too large"
	errImageBad        = "image format unsupported"
	errProtocolInput   = "protocol cannot embed this input type"
	errStalled         = "timed out or interrupted, retry"
	errGeneric         = "vectorization failed, retry later"
	errQueueBusy       = "queue unavailable, retry later"
)

// summarize maps an indexing error to its user-safe summary.
func summarize(err error) string {
	switch {
	case err == nil:
		return ""
	case errors.Is(err, context.Canceled):
		return errCanceled
	case errors.Is(err, context.DeadlineExceeded):
		return errTimedOut
	case errors.Is(err, errNotConfigured):
		return errServiceConf
	case errors.Is(err, ErrProviderUnavailable):
		return errServiceDownMsg
	case errors.Is(err, errQueue):
		return errQueueBusy
	case errors.Is(err, errNoContent):
		return errNoUsable
	case errors.Is(err, errConfigChanged):
		return errConfigResubmit
	case errors.Is(err, errImageTooLarge):
		return errImageBig
	case errors.Is(err, errImageFormat):
		return errImageBad
	case errors.Is(err, ErrModalityUnsupported):
		return errProtocolInput
	}
	return errGeneric
}

// retriable reports whether a failed run is worth another delivery.
func retriable(err error) bool {
	switch {
	case errors.Is(err, errNoContent), errors.Is(err, errImageTooLarge), errors.Is(err, errImageFormat),
		errors.Is(err, ErrModalityUnsupported), errors.Is(err, errNotConfigured), errors.Is(err, errConfigChanged),
		errors.Is(err, context.Canceled):
		return false
	}
	return true
}

func errAttr(err error) logx.Field { return logx.Err(err) }
