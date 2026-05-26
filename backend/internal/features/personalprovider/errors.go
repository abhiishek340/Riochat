package personalprovider

import (
	"errors"
	"net/http"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Error codes are part of the public API; the messages are matched by clients.
var (
	ErrDisabled          = apperr.Define("personal_provider.disabled", "personal model providers are not enabled for this account")
	ErrNotFound          = apperr.Define("personal_provider.not_found", "model provider not found")
	ErrLimitReached      = apperr.Define("personal_provider.limit_reached", "model provider limit reached")
	ErrDuplicate         = apperr.Define("personal_provider.duplicate", "this API key is already added for this address")
	ErrSuspended         = apperr.Define("personal_provider.suspended", "this model provider was disabled by an administrator")
	ErrInvalidName       = apperr.Define("personal_provider.invalid_name", "invalid model provider name")
	ErrInvalidIcon       = apperr.Define("personal_provider.invalid_icon", "invalid model provider icon")
	ErrInvalidProtocol   = apperr.Define("personal_provider.invalid_protocol", "unsupported model provider protocol")
	ErrInvalidBaseURL    = apperr.Define("personal_provider.invalid_base_url", "invalid model provider address")
	ErrBlockedHost       = apperr.Define("personal_provider.blocked_host", "this address is not allowed")
	ErrInvalidAPIKey     = apperr.Define("personal_provider.invalid_api_key", "invalid API key")
	ErrInvalidModels     = apperr.Define("personal_provider.invalid_models", "invalid model selection")
	ErrInvalidModelProto = apperr.Define("personal_provider.invalid_model_protocol", "unsupported protocol for this model")
	ErrUpstreamRejected  = apperr.Define("personal_provider.upstream_rejected", "the provider rejected the request; check the API key")
	ErrUpstreamDown      = apperr.Define("personal_provider.upstream_unavailable", "could not reach the model provider")
	ErrModelUnavailable  = apperr.Define("personal_provider.model_unavailable", "this personal model is not available")
)

// Runtime sentinels for the conversation area. They are plain errors: the
// conversation area owns the public codes for these conditions.
var (
	// ErrAllRoutesUnavailable is returned when a caller excluded route ids after
	// a failure: a personal model has exactly one route and never falls back.
	ErrAllRoutesUnavailable = errors.New("personalprovider: all routes unavailable")
	// ErrNoRouteForTask is returned when none of the model's protocols can serve
	// the requested task.
	ErrNoRouteForTask = errors.New("personalprovider: no route for task")
)

// statusFor maps a typed error of this area to its HTTP status; 0 when the
// error is not one of ours.
func statusFor(err error) int {
	te, ok := apperr.Find(err)
	if !ok {
		return 0
	}
	switch te.Code {
	case ErrDisabled.Code:
		return http.StatusForbidden
	case ErrNotFound.Code:
		return http.StatusNotFound
	case ErrLimitReached.Code, ErrDuplicate.Code, ErrSuspended.Code:
		return http.StatusConflict
	case ErrInvalidName.Code, ErrInvalidIcon.Code, ErrInvalidProtocol.Code, ErrInvalidBaseURL.Code,
		ErrBlockedHost.Code, ErrInvalidAPIKey.Code, ErrInvalidModels.Code, ErrInvalidModelProto.Code:
		return http.StatusBadRequest
	case ErrUpstreamRejected.Code, ErrUpstreamDown.Code:
		return http.StatusBadGateway
	case ErrModelUnavailable.Code:
		return http.StatusServiceUnavailable
	case apperr.ErrInvalidRequest.Code:
		return http.StatusBadRequest
	}
	return 0
}

// asTyped returns the code of the typed error in err's chain.
func asTyped(err error) (string, bool) {
	te, ok := apperr.Find(err)
	if !ok {
		return "", false
	}
	return te.Code, true
}
