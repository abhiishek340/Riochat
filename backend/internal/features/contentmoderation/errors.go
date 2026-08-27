package contentmoderation

import (
	"net/http"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Errors returned by the admin operations. The codes are API contracts that the
// web client maps to messages.
var (
	ErrSuperAdminRequired = apperr.Define("auth.superadmin_required", "superadmin permission required").WithStatus(http.StatusForbidden)
	ErrAdminRequired      = apperr.ErrAdminRequired.WithStatus(http.StatusForbidden)
	ErrInvalidConfig      = apperr.Define("content_moderation.invalid_config", "invalid content moderation config").WithStatus(http.StatusBadRequest)
	ErrConfigRequired     = apperr.Define("content_moderation.config_required", "content moderation requires a base url, model, api key and at least one category").WithStatus(http.StatusBadRequest)
	// ErrProbeFailed is defined so the code is accepted at the HTTP boundary;
	// a failed probe target is reported inside a 200 response instead.
	ErrProbeFailed       = apperr.Define("content_moderation.probe_failed", "content moderation probe failed").WithStatus(http.StatusBadRequest)
	ErrEventNotFound     = apperr.Define("content_moderation.event_not_found", "content moderation event not found").WithStatus(http.StatusNotFound)
	ErrInvalidUserID     = apperr.Define("request.invalid_userid", "invalid userid").WithStatus(http.StatusBadRequest)
	ErrInvalidImageIndex = apperr.Define("request.invalid_image_index", "invalid image index").WithStatus(http.StatusBadRequest)
	ErrInvalidQuery      = apperr.ErrInvalidQuery.WithStatus(http.StatusBadRequest)
)

// Error code stored on messages withdrawn by a block (not an HTTP code).
const (
	BlockedErrorCode    = "content_moderation.blocked"
	BlockedErrorMessage = "content blocked by moderation"
)

func init() {
	apperr.Register(BlockedErrorCode, BlockedErrorMessage)
}
