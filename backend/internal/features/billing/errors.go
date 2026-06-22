package billing

import (
	"net/http"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Error codes are an API contract (the front end maps them to messages). Each
// carries the default HTTP status of the response it produces; handlers
// override the status only where the contract varies by caller.
var (
	ErrSubscribeFailed         = apperr.Define("billing.subscribe_failed", "subscription failed").WithStatus(http.StatusInternalServerError)
	ErrPeriodCreditExceeded    = apperr.Define("billing.period_credit_exceeded", "period usage credit exceeded").WithStatus(http.StatusPaymentRequired)
	ErrPricingRequired         = apperr.Define("billing.pricing_required", "model pricing is required").WithStatus(http.StatusPaymentRequired)
	ErrInvalidModelPricing     = apperr.Define("billing.invalid_model_pricing", "invalid model pricing").WithStatus(http.StatusBadRequest)
	ErrPaymentRequired         = apperr.Define("billing.payment_required", "payment is required").WithStatus(http.StatusPaymentRequired)
	ErrInsufficientFunds       = apperr.Define("billing.insufficient_funds", "insufficient balance").WithStatus(http.StatusPaymentRequired)
	ErrReservationConflict     = apperr.Define("billing.reservation_conflict", "usage request already exists").WithStatus(http.StatusConflict)
	ErrConcurrencyLimit        = apperr.Define("billing.concurrency_limit_exceeded", "too many concurrent paid requests").WithStatus(http.StatusTooManyRequests)
	ErrInvalidSubscriptionTier = apperr.Define("billing.invalid_subscription_tier", "invalid subscription tier").WithStatus(http.StatusBadRequest)
	ErrSubscriptionExpiryReq   = apperr.Define("billing.subscription_expiry_required", "subscription expiry required").WithStatus(http.StatusBadRequest)
	ErrInvalidSubscriptionExp  = apperr.Define("billing.invalid_subscription_expiry", "invalid subscription expiry").WithStatus(http.StatusBadRequest)
	ErrInvalidPlan             = apperr.Define("billing.invalid_plan", "invalid billing plan").WithStatus(http.StatusBadRequest)
	ErrPlanNotFound            = apperr.Define("billing.plan_not_found", "billing plan not found").WithStatus(http.StatusNotFound)
	ErrInvalidPermissionGroup  = apperr.Define("billing.invalid_permission_group", "invalid permission group").WithStatus(http.StatusBadRequest)
	ErrEntitlementActive       = apperr.Define("billing.subscription_entitlement_active", "subscription entitlement is active").WithStatus(http.StatusBadRequest)
	ErrRedemptionSecret        = apperr.Define("billing.redemption_secret_unavailable", "redemption code service is unavailable").WithStatus(http.StatusInternalServerError)
	ErrInvalidRedemptionCode   = apperr.Define("billing.invalid_redemption_code", "invalid redemption code").WithStatus(http.StatusBadRequest)
	ErrRedemptionConflict      = apperr.Define("billing.redemption_code_conflict", "redemption code already exists").WithStatus(http.StatusConflict)
	ErrRedemptionUnavailable   = apperr.Define("billing.redemption_code_unavailable", "redemption code is unavailable").WithStatus(http.StatusBadRequest)
	ErrRedemptionNoPlaintext   = apperr.Define("billing.redemption_code_plaintext_unavailable", "redemption code plaintext unavailable").WithStatus(http.StatusBadRequest)
	ErrRedemptionExhausted     = apperr.Define("billing.redemption_code_exhausted", "redemption code exhausted").WithStatus(http.StatusBadRequest)
	ErrRedemptionUserLimit     = apperr.Define("billing.redemption_user_limit_exceeded", "redemption user limit exceeded").WithStatus(http.StatusBadRequest)
	ErrInvalidAccountBalance   = apperr.Define("billing.invalid_account_balance", "invalid billing account balance").WithStatus(http.StatusBadRequest)
	ErrInvalidNativeToolPrice  = apperr.Define("billing.invalid_native_tool_pricing", "invalid native tool pricing").WithStatus(http.StatusBadRequest)
	ErrInvalidDailyRange       = apperr.Define("billing.invalid_daily_usage_date_range", "invalid daily usage date range").WithStatus(http.StatusBadRequest)
	ErrInvalidDailyDays        = apperr.Define("billing.invalid_daily_usage_days", "invalid daily usage days").WithStatus(http.StatusBadRequest)

	ErrProviderUnavailable   = apperr.Define("payment.provider_unavailable", "payment provider is unavailable").WithStatus(http.StatusBadRequest)
	ErrEpayTypeUnsupported   = apperr.Define("payment.epay_type_unsupported", "epay payment type is not supported").WithStatus(http.StatusBadRequest)
	ErrEpayGatewayInvalid    = apperr.Define("payment.epay_gateway_invalid", "epay gateway url is invalid").WithStatus(http.StatusServiceUnavailable)
	ErrCheckoutFailed        = apperr.Define("payment.checkout_failed", "create checkout failed").WithStatus(http.StatusBadGateway)
	ErrReturnURLInvalid      = apperr.Define("payment.return_url_invalid", "payment return url is invalid").WithStatus(http.StatusBadRequest)
	ErrReturnURLCrossOrigin  = apperr.Define("payment.return_url_cross_origin", "payment return url must use the configured public web origin").WithStatus(http.StatusBadRequest)
	ErrInvalidOrder          = apperr.Define("payment.invalid_order", "invalid payment order").WithStatus(http.StatusBadRequest)
	ErrOrderNotFound         = apperr.Define("payment.order_not_found", "payment order not found").WithStatus(http.StatusBadRequest)
	ErrOrderStateInvalid     = apperr.Define("payment.order_state_invalid", "payment order state is invalid").WithStatus(http.StatusBadRequest)
	ErrNotificationMismatch  = apperr.Define("payment.notification_mismatch", "payment notification does not match the order").WithStatus(http.StatusBadRequest)
	ErrInvalidSignature      = apperr.Define("payment.invalid_signature", "invalid stripe signature").WithStatus(http.StatusBadRequest)
	ErrInvalidEvent          = apperr.Define("payment.invalid_event", "invalid stripe event").WithStatus(http.StatusBadRequest)
	ErrInvalidWebhookBody    = apperr.Define("payment.invalid_webhook_body", "invalid webhook body").WithStatus(http.StatusBadRequest)
	ErrWebhookBodyTooLarge   = apperr.Define("payment.webhook_body_too_large", "webhook body too large").WithStatus(http.StatusRequestEntityTooLarge)
	ErrWebhookNotConfigured  = apperr.Define("payment.webhook_not_configured", "stripe webhook is not configured").WithStatus(http.StatusBadRequest)
	ErrOrderNoRequired       = apperr.Define("payment.order_no_required", "order_no is required").WithStatus(http.StatusBadRequest)
	ErrInvalidPlanID         = apperr.Define("plan.invalid_id", "invalid plan id").WithStatus(http.StatusBadRequest)
	ErrInvalidRedemptionID   = apperr.Define("redemption_code.invalid_id", "invalid redemption code id").WithStatus(http.StatusBadRequest)
	ErrInvalidUserID         = apperr.Define("user.invalid_id", "invalid user id").WithStatus(http.StatusBadRequest)
	ErrPlatformModelRequired = apperr.Define("llm.platform_model_name_required", "platform model name is required").WithStatus(http.StatusBadRequest)
	ErrSettingsUnavailable   = apperr.Define("settings.service_unavailable", "settings service unavailable").WithStatus(http.StatusInternalServerError)
	ErrSettingsInvalidValue  = apperr.Define("settings.invalid_value", "invalid setting value").WithStatus(http.StatusBadRequest)
	ErrBillingPaymentInvalid = apperr.Define("settings.billing_payment_invalid", "invalid billing payment settings").WithStatus(http.StatusBadRequest)

	ErrStatsInvalidRange   = apperr.Define("usage_statistics.invalid_date_range", "invalid usage statistics date range").WithStatus(http.StatusBadRequest)
	ErrStatsInvalidScope   = apperr.Define("usage_statistics.invalid_billing_scope", "invalid billing scope").WithStatus(http.StatusBadRequest)
	ErrStatsInvalidSection = apperr.Define("usage_statistics.invalid_section", "invalid usage statistics section").WithStatus(http.StatusBadRequest)
	ErrStatsInvalidRank    = apperr.Define("usage_statistics.invalid_rank_by", "invalid usage ranking field").WithStatus(http.StatusBadRequest)
	ErrStatsSubject        = apperr.Define("usage_statistics.subject_conflict", "user and permission group filters are mutually exclusive").WithStatus(http.StatusBadRequest)

	// ErrNotFoundGeneric is used for plain 404s (unknown redemption code on
	// admin routes already uses ErrRedemptionUnavailable with status 404).
	ErrUpstreamUnavailable = apperr.Define("upstream.unavailable", "upstream service unavailable").WithStatus(http.StatusBadGateway)
)

// featureLocked builds the 404 feature.disabled error naming the billing
// capability and, optionally, the settings keys that could not be changed.
func featureLocked(keys ...string) error {
	d := map[string]any{"feature": "billingGating"}
	if len(keys) > 0 {
		d["keys"] = keys
	}
	return apperr.ErrFeatureDisabled.WithDetails(d).WithStatus(http.StatusNotFound)
}

// invalidRedemption builds the validation error with {field, reason} details.
func invalidRedemption(field, reason string) error {
	return ErrInvalidRedemptionCode.WithDetails(map[string]any{"field": field, "reason": reason})
}

// typedErr is the platform's typed application error.
type typedErr = apperr.Error
