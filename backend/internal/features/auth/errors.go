package auth

import (
	"net/http"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// def declares a typed error together with its default HTTP status. Handlers
// answer through httpx.FailErr, which honors the status hint; call sites that
// need another status use WithStatus.
func def(status int, code, message string) *apperr.Error {
	return apperr.Define(code, message).WithStatus(status)
}

// Credentials, tokens and sessions.
var (
	ErrInvalidCredentials     = def(http.StatusUnauthorized, "auth.invalid_credentials", "invalid username or password")
	ErrAccountLocked          = def(http.StatusLocked, "auth.account_locked", "account is temporarily locked, try again later")
	ErrInvalidRefreshToken    = def(http.StatusUnauthorized, "auth.invalid_refresh_token", "invalid refresh token")
	ErrInvalidLocalGrant      = def(http.StatusUnauthorized, "auth.invalid_local_grant", "invalid local grant")
	ErrInvalidCurrentPassword = def(http.StatusUnauthorized, "auth.invalid_current_password", "invalid current password")
	ErrInvalidPassword        = def(http.StatusBadRequest, "auth.invalid_password", "password must be at least 8 characters and not digits only")
	ErrPasswordReuse          = def(http.StatusBadRequest, "auth.password_reuse_not_allowed", "new password must be different from the bootstrap password")
	ErrPasswordResetFailed    = def(http.StatusBadRequest, "auth.password_reset_failed", "password reset failed")
	ErrUsernameChangeRequired = def(http.StatusBadRequest, "auth.username_change_required", "username change required")
	ErrUnauthorized           = def(http.StatusUnauthorized, "auth.unauthorized", "unauthorized")
	ErrSessionInvalid         = def(http.StatusUnauthorized, "auth.session_invalid", "session invalid")
	ErrBadRequest             = def(http.StatusBadRequest, "request.invalid", "invalid request")
	ErrInternal               = def(http.StatusInternalServerError, "internal.error", "internal server error")
)

// Second factor.
var (
	ErrInvalidTwoFactorCode      = def(http.StatusUnauthorized, "auth.invalid_two_factor_code", "invalid two factor code")
	ErrTwoFactorExpired          = def(http.StatusUnauthorized, "auth.two_factor_expired", "two factor challenge expired")
	ErrTwoFactorNotStarted       = def(http.StatusBadRequest, "auth.two_factor_not_started", "two factor setup not started")
	ErrTwoFactorAlreadyEnabled   = def(http.StatusBadRequest, "auth.two_factor_already_enabled", "two factor authentication is already enabled")
	ErrInvalidTwoFactorChallenge = def(http.StatusUnauthorized, "request.invalid_two_factor_challenge", "invalid two factor challenge")
)

// Verification codes and email.
var (
	ErrVerificationMethodUnavailable = def(http.StatusBadRequest, "auth.verification_method_unavailable", "verification method is unavailable")
	ErrVerificationCodeInvalid       = def(http.StatusBadRequest, "auth.verification_code_invalid", "verification code is invalid or expired")
	ErrVerificationCodeRecent        = def(http.StatusBadRequest, "auth.verification_code_recent", "verification code was sent recently")
	ErrVerificationAttemptsExceeded  = def(http.StatusBadRequest, "auth.verification_code_attempts_exceeded", "verification code attempts exceeded")
	ErrInvalidEmail                  = def(http.StatusBadRequest, "auth.invalid_email", "invalid email")
	ErrEmailAlreadyExists            = def(http.StatusBadRequest, "auth.email_already_exists", "email already exists")
	ErrEmailNotVerified              = def(http.StatusBadRequest, "auth.email_not_verified", "email is not verified")
	ErrEmailUnchanged                = def(http.StatusBadRequest, "auth.email_unchanged", "new email must be different")
	ErrEmailAliasNotAllowed          = def(http.StatusBadRequest, "auth.email_alias_not_allowed", "email aliases are not allowed")
	ErrEmailDomainNotAllowed         = def(http.StatusBadRequest, "auth.email_domain_not_allowed", "email domain is not allowed")
	ErrEmailBootstrapNotAllowed      = def(http.StatusBadRequest, "auth.email_bootstrap_not_allowed", "email bootstrap is not allowed")
	ErrEmailRegistrationDisabled     = def(http.StatusBadRequest, "auth.email_registration_disabled", "email registration is disabled")
	ErrEmailVerificationDisabled     = def(http.StatusBadRequest, "auth.email_verification_disabled", "email verification is disabled")
	ErrTurnstileNotConfigured        = def(http.StatusBadRequest, "auth.turnstile_not_configured", "turnstile is not configured")
	ErrTurnstileRequired             = def(http.StatusBadRequest, "auth.turnstile_required", "turnstile verification is required")
	ErrTurnstileInvalid              = def(http.StatusBadRequest, "auth.turnstile_invalid", "turnstile verification failed")
	ErrSMTPInvalid                   = def(http.StatusBadRequest, "settings.smtp_invalid", "smtp is not configured")
)

// Identity providers and the provider bridge.
var (
	ErrProviderInvalid             = def(http.StatusBadRequest, "auth.provider_invalid", "provider authentication failed")
	ErrProviderLoginDisabled       = def(http.StatusBadRequest, "auth.provider_login_disabled", "provider login is disabled")
	ErrProviderRegistrationOff     = def(http.StatusBadRequest, "auth.provider_registration_disabled", "provider registration is disabled")
	ErrProviderRegistrationNeedsLo = def(http.StatusBadRequest, "auth.provider_registration_requires_login", "provider registration requires login to be enabled")
	ErrProviderBindEndpoint        = def(http.StatusBadRequest, "auth.provider_bind_endpoint_required", "provider bind endpoint is required")
	ErrProviderIDRequired          = def(http.StatusBadRequest, "auth.provider_id_required", "provider id is required")
	ErrProviderOrderInvalid        = def(http.StatusBadRequest, "auth.provider_order_invalid", "provider ids must be unique")
	ErrProviderTypeInvalid         = def(http.StatusBadRequest, "auth.provider_type_invalid", "provider type is invalid")
	ErrProviderNameRequired        = def(http.StatusBadRequest, "auth.provider_name_required", "provider name is required")
	ErrProviderSlugRequired        = def(http.StatusBadRequest, "auth.provider_slug_required", "provider slug is required")
	ErrProviderDefaultRoleInvalid  = def(http.StatusBadRequest, "auth.provider_default_role_invalid", "provider default role is invalid")
	ErrProviderLogoURLInvalid      = def(http.StatusBadRequest, "auth.provider_logo_url_invalid", "provider logo url is invalid")
	ErrProviderClientIDRequired    = def(http.StatusBadRequest, "auth.provider_client_id_required", "provider client id is required")
	ErrProviderClientSecretReq     = def(http.StatusBadRequest, "auth.provider_client_secret_required", "provider client secret is required")
	ErrProviderOIDCIssuerRequired  = def(http.StatusBadRequest, "auth.provider_oidc_issuer_required", "oidc provider requires an issuer url or a discovery url")
	ErrProviderOAuthURLsRequired   = def(http.StatusBadRequest, "auth.provider_oauth_urls_required", "oauth2 provider requires auth, token and userinfo urls")
	ErrProviderAuthURLMissing      = def(http.StatusBadRequest, "auth.provider_auth_url_not_configured", "provider authorization url is not configured")
	ErrProviderSubjectMissing      = def(http.StatusBadRequest, "auth.provider_subject_missing", "provider did not return a subject")
	ErrProviderIdentityConflict    = def(http.StatusBadRequest, "auth.provider_identity_conflict", "identity is already bound to another account")
	ErrProviderAlreadyBound        = def(http.StatusBadRequest, "auth.provider_already_bound", "provider is already bound to this account")
	ErrProviderAccountNotReg       = def(http.StatusBadRequest, "auth.provider_account_not_registered", "no account is registered for this identity")
	ErrProviderEmailConflict       = def(http.StatusConflict, "auth.provider_email_conflict", "provider email belongs to another account")
	ErrProviderUpstreamFailed      = def(http.StatusBadRequest, "auth.provider_upstream_failed", "provider authentication failed")
	ErrProviderCallbackMisconf     = def(http.StatusBadRequest, "auth.provider_callback_misconfigured", "provider callback url is misconfigured")
	ErrProviderSuperadminRole      = def(http.StatusForbidden, "auth.provider_superadmin_default_role_protected", "only a superadmin can assign the superadmin role")
	ErrOAuthStateInvalid           = def(http.StatusBadRequest, "auth.oauth_state_invalid", "sign-in state is invalid")
	ErrOAuthStateExpired           = def(http.StatusBadRequest, "auth.oauth_state_expired", "sign-in state expired")
	ErrInvalidRedirectURI          = def(http.StatusBadRequest, "auth.invalid_redirect_uri", "invalid redirect uri")
	ErrInvalidPKCE                 = def(http.StatusBadRequest, "auth.invalid_pkce", "invalid sign-in verification parameters")
	ErrLastLoginMethod             = def(http.StatusBadRequest, "auth.last_login_method_required", "set a password or bind another identity provider first")
	ErrIdentityNotFound            = def(http.StatusNotFound, "identity.not_found", "identity not found")
	ErrIdentityInvalidID           = def(http.StatusBadRequest, "identity.invalid_id", "invalid identity id")
	ErrProviderDeleteConflict      = def(http.StatusConflict, "identity_provider.delete_conflict", "deleting this identity provider would remove the only login method for some users")
)

// Profile and account.
var (
	ErrInvalidUsername          = def(http.StatusBadRequest, "user.invalid_username", "invalid username")
	ErrUsernameExists           = def(http.StatusConflict, "user.username_already_exists", "username already exists")
	ErrUsernameChangeUsed       = def(http.StatusConflict, "user.username_change_used", "username change already used")
	ErrInvalidDisplayName       = def(http.StatusBadRequest, "user.invalid_display_name", "invalid display name")
	ErrInvalidAvatarURL         = def(http.StatusBadRequest, "user.invalid_avatar_url", "invalid avatar url")
	ErrInvalidTimeZone          = def(http.StatusBadRequest, "user.invalid_time_zone", "invalid time zone")
	ErrInvalidLocale            = def(http.StatusBadRequest, "user.invalid_locale", "invalid user locale")
	ErrInvalidLocation          = def(http.StatusBadRequest, "request.invalid_location_payload", "invalid location payload")
	ErrInvalidAppearance        = def(http.StatusBadRequest, "request.invalid_appearance_preferences", "invalid appearance preferences")
	ErrSuperadminDelete         = def(http.StatusForbidden, "user.superadmin_delete_protected", "superadmin account cannot be deleted")
	ErrDeleteVerificationNeeded = def(http.StatusBadRequest, "user.account_delete_verification_required", "account deletion requires verification")
	ErrOwnerFileReference       = def(http.StatusConflict, "knowledge_base.owner_file_reference", "user owns files referenced by builtin knowledge bases")
)
