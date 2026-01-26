package user

import (
	"errors"
	"net/http"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// def declares a typed error with its default HTTP status.
func def(status int, code, message string) *apperr.Error {
	return apperr.Define(code, message).WithStatus(status)
}

// Account field and lookup errors.
var (
	ErrNotFound           = def(http.StatusNotFound, "user.not_found", "user not found")
	ErrInvalidID          = def(http.StatusBadRequest, "user.invalid_id", "invalid user id")
	ErrInvalidUsername    = def(http.StatusBadRequest, "user.invalid_username", "invalid username")
	ErrUsernameExists     = def(http.StatusConflict, "user.username_already_exists", "username already exists")
	ErrInvalidDisplayName = def(http.StatusBadRequest, "user.invalid_display_name", "invalid display name")
	ErrInvalidPassword    = def(http.StatusBadRequest, "auth.invalid_password", "password must be at least 8 characters and not digits only")
	ErrInvalidEmail       = def(http.StatusBadRequest, "user.invalid_email", "invalid user email")
	ErrInvalidPhone       = def(http.StatusBadRequest, "user.invalid_phone", "invalid user phone")
	ErrInvalidLocale      = def(http.StatusBadRequest, "user.invalid_locale", "invalid user locale")
	ErrInvalidTimeZone    = def(http.StatusBadRequest, "user.invalid_time_zone", "invalid time zone")
	ErrInvalidAvatarURL   = def(http.StatusBadRequest, "user.invalid_avatar_url", "invalid avatar url")
)

// Subscription errors raised while an administrator creates a user.
var (
	ErrInvalidSubscriptionTier   = def(http.StatusBadRequest, "billing.invalid_subscription_tier", "invalid subscription tier")
	ErrSubscriptionExpiryMissing = def(http.StatusBadRequest, "billing.subscription_expiry_required", "subscription expiry required")
	ErrInvalidSubscriptionExpiry = def(http.StatusBadRequest, "billing.invalid_subscription_expiry", "invalid subscription expiry")
)

// HTTP-level errors of this area.
var (
	ErrAvatarNotFound     = def(http.StatusNotFound, "avatar.not_found", "avatar not found")
	ErrInvalidActivityDay = def(http.StatusBadRequest, "request.invalid_daily_activity_days", "invalid daily activity days")
	ErrUnknownSettingKey  = def(http.StatusBadRequest, "user_settings.unknown_key", "unknown setting key")
	ErrInvalidSettingVal  = def(http.StatusBadRequest, "user_settings.invalid_value", "invalid user setting value")
	ErrInternal           = def(http.StatusInternalServerError, "internal.error", "internal server error")
)

// ErrOwnerFileReference refuses an account deletion because the user owns
// files that a built-in knowledge base still references.
var ErrOwnerFileReference = def(http.StatusConflict, "knowledge_base.owner_file_reference", "user owns files referenced by builtin knowledge bases")

// Storage-level outcomes that callers (the admin and auth areas) translate into
// their own errors. They are plain sentinels so that a caller that forgets to
// map one surfaces a 500 instead of leaking a misleading public code.
var (
	// ErrLastSuperadmin: the update would demote the only remaining superadmin.
	ErrLastSuperadmin = errors.New("user: cannot demote the last superadmin")
	// ErrDuplicateIdentity: the (provider, subject) pair is already linked.
	ErrDuplicateIdentity = errors.New("user: identity already linked")
	// ErrDuplicate: some other unique constraint was violated.
	ErrDuplicate = errors.New("user: duplicate record")
)
