package audit

import (
	"net/http"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Errors of the shared log-cleanup flow (audit portion).
var (
	ErrInvalidCleanupBefore = apperr.Define("request.invalid_log_cleanup_before", "invalid log cleanup before").WithStatus(http.StatusBadRequest)
	ErrInvalidCleanupType   = apperr.Define("request.invalid_log_cleanup_type", "invalid log cleanup type").WithStatus(http.StatusBadRequest)
	ErrCleanupBeforeFuture  = apperr.Define("log_cleanup.before_in_future", "log cleanup before must not be in the future").WithStatus(http.StatusBadRequest)
)
