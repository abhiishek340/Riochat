package announcement

import "github.com/riochat/riochat/backend/internal/platform/apperr"

// Error codes are part of the client contract.
var (
	ErrInvalidID           = apperr.Define("announcement.invalid_id", "invalid announcement id")
	ErrNotFound            = apperr.Define("announcement.not_found", "announcement not found")
	ErrInvalidAnnouncement = apperr.Define("request.invalid_announcement", "invalid announcement")
)
