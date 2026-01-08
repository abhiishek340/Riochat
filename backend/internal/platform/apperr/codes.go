package apperr

// Boundary codes owned by the platform. Feature packages register their own
// codes with Define; they must not edit this file.
var (
	ErrInvalidBody     = Define("request.invalid_body", "invalid request body")
	ErrInvalidRequest  = Define("request.invalid", "invalid request")
	ErrInvalidID       = Define("request.invalid_id", "invalid id")
	ErrInvalidQuery    = Define("request.invalid_query", "invalid query parameter")
	ErrRequired        = Define("request.required", "required field missing")
	ErrUnauthorized    = Define("auth.unauthorized", "unauthorized")
	ErrForbidden       = Define("auth.forbidden", "forbidden")
	ErrInvalidToken    = Define("auth.invalid_token", "invalid token")
	ErrSessionInvalid  = Define("auth.session_invalid", "session invalid")
	ErrAdminRequired   = Define("auth.admin_required", "admin permission required")
	ErrNotFound        = Define("resource.not_found", "resource not found")
	ErrConflict        = Define("resource.conflict", "resource conflict")
	ErrFeatureDisabled = Define("feature.disabled", "this feature is not available on this server")
	ErrPaymentRequired = Define("billing.payment_required", "payment required")
	ErrRateLimited     = Define("rate_limit.exceeded", "rate limit exceeded")
	ErrRefreshLimited  = Define("rate_limit.refresh_exceeded", "too many refresh attempts")
	ErrAuthLimited     = Define("rate_limit.authentication_exceeded", "too many authentication attempts")
	ErrOriginForbidden = Define("cors.origin_forbidden", "origin is not allowed")
	ErrUpstream        = Define("upstream.unavailable", "upstream service unavailable")
	ErrUnavailable     = Define("service.unavailable", "service unavailable")
	ErrInternal        = Define("internal.error", "internal server error")
)

func init() {
	for code, msg := range map[string]string{
		"auth.invalid_credentials":                      "invalid username or password",
		"auth.invalid_current_password":                 "invalid current password",
		"auth.invalid_refresh_token":                    "invalid refresh token",
		"auth.invalid_two_factor_code":                  "invalid two factor code",
		"auth.two_factor_expired":                       "two factor challenge expired",
		"auth.two_factor_not_started":                   "two factor setup not started",
		"auth.last_login_method_required":               "set a password or bind another identity provider first",
		"auth.provider_email_conflict":                  "provider email belongs to another account",
		"billing.insufficient_funds":                    "insufficient balance",
		"billing.pricing_required":                      "model pricing is required",
		"billing.invalid_redemption_code":               "invalid redemption code",
		"quota.exceeded":                                "quota exceeded",
		"file.in_use":                                   "file is in use",
		"file.too_large":                                "file too large",
		"file.not_ready":                                "file is not ready",
		"file.type_blocked":                             "file type is not allowed",
		"file.not_found":                                "file not found",
		"upstream.rate_limited":                         "upstream rate limited",
		"conversation.message_fork_history_incomplete":  "message history is too deep or incomplete",
		"conversation.message_fork_state_invalid":       "message is still generating",
		"conversation.message_fork_target_invalid":      "only assistant messages can be forked",
		"embedding.service_not_configured":              "embedding service is not configured",
		"embedding.service_unavailable":                 "embedding service is not available",
		"embedding.submit_failed":                       "failed to submit embedding jobs",
		"embedding.too_many_files":                      "too many files for embedding",
		"identity_provider.delete_conflict":             "deleting this identity provider would remove the only login method for some users",
		"knowledge_base.conflict":                       "knowledge base conflict",
		"knowledge_base.disabled":                       "knowledge base feature is disabled",
		"knowledge_base.file_cleanup_unavailable":       "platform file cleanup unavailable",
		"knowledge_base.internal":                       "knowledge base operation failed",
		"knowledge_base.invalid":                        "invalid knowledge base request",
		"knowledge_base.not_found":                      "knowledge base not found",
		"knowledge_base.owner_file_reference":           "user owns files referenced by builtin knowledge bases",
		"knowledge_base.platform_file_in_use":           "platform file is in use",
		"llm.empty_response":                            "model returned empty response",
		"llm.model_icon_asset_in_use":                   "model icon asset is in use",
		"llm.model_vendor_builtin":                      "built-in model vendor cannot be deleted",
		"llm.model_vendor_in_use":                       "model vendor is in use",
		"llm.remote_models_empty_confirmation_required": "remote models snapshot is empty",
		"llm.remote_models_snapshot_changed":            "remote models snapshot changed",
		"llm.upstream_model_binding_changed":            "upstream model binding changed; reload and retry",
		"llm.upstream_model_conflict":                   "model upstream source conflict",
		"media.artifact_unavailable":                    "generated media artifact is temporarily unavailable",
		"media.image_stream_unsupported":                "upstream may not support image streaming; disable image.stream for this model",
		"payment.checkout_failed":                       "create checkout failed",
		"payment.epay_gateway_invalid":                  "epay gateway url is invalid",
		"payment.provider_unavailable":                  "payment provider is unavailable",
		"settings.invalid_namespace":                    "invalid setting namespace",
		"settings.invalid_key":                          "invalid setting key",
		"settings.invalid_value":                        "invalid setting value",
		"settings.smtp_invalid":                         "invalid SMTP settings",
		"settings.billing_payment_invalid":              "invalid billing payment settings",
		"settings.embedding_invalid":                    "invalid embedding settings",
		"settings.extract_invalid":                      "invalid file extraction settings",
		"settings.model_option_policy_invalid":          "invalid model option policy settings",
		"skill.too_many_ids":                            "too many skill ids",
		"usage_statistics.invalid_billing_scope":        "invalid billing scope",
		"usage_statistics.invalid_date_range":           "invalid usage statistics date range",
		"usage_statistics.invalid_rank_by":              "invalid usage ranking field",
		"usage_statistics.invalid_section":              "invalid usage statistics section",
		"usage_statistics.subject_conflict":             "user and permission group filters are mutually exclusive",
	} {
		Register(code, msg)
	}
}
