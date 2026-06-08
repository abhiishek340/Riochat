package channel

import (
	"net/http"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Error sentinels. The codes and messages are the front-end i18n contract; the
// status hint is what the HTTP layer answers with (httpx.FailErr). Callers that
// need a different status for the same code (for example a vendor lookup that
// fails while creating a model answers 400, not 404) re-annotate with
// WithStatus.
var (
	ErrInvalidUpstreamID     = apperr.Define("upstream.invalid_id", "invalid upstream id").WithStatus(http.StatusBadRequest)
	ErrInvalidRouteID        = apperr.Define("route.invalid_id", "invalid route id").WithStatus(http.StatusBadRequest)
	ErrInvalidModelID        = apperr.Define("model.invalid_id", "invalid model id").WithStatus(http.StatusBadRequest)
	ErrInvalidGroupID        = apperr.Define("model_display_group.invalid_id", "invalid model display group id").WithStatus(http.StatusBadRequest)
	ErrInvalidSettingKey     = apperr.Define("settings.invalid_key", "invalid setting key").WithStatus(http.StatusBadRequest)
	ErrUpstreamNotFound      = apperr.Define("upstream.not_found", "upstream not found").WithStatus(http.StatusNotFound)
	ErrModelNotFound         = apperr.Define("model.not_found", "model not found").WithStatus(http.StatusNotFound)
	ErrUpstreamModelNotFound = apperr.Define("upstream_model.not_found", "upstream model not found").WithStatus(http.StatusNotFound)
	ErrSourcesNotFound       = apperr.Define("model_upstream_sources.not_found", "model upstream sources not found").WithStatus(http.StatusNotFound)
	ErrVendorNotFound        = apperr.Define("model_vendor.not_found", "model vendor not found").WithStatus(http.StatusNotFound)
	ErrGroupNotFound         = apperr.Define("model_display_group.not_found", "model display group not found").WithStatus(http.StatusNotFound)
	ErrSettingNotFound       = apperr.Define("settings.not_found", "setting not found").WithStatus(http.StatusNotFound)

	ErrPlatformNameExists    = apperr.Define("llm.platform_model_name_exists", "platform model name already exists").WithStatus(http.StatusConflict)
	ErrRouteConflict         = apperr.Define("llm.route_conflict", "target model is already bound on this upstream").WithStatus(http.StatusConflict)
	ErrUpstreamModelConflict = apperr.Define("llm.upstream_model_conflict", "model upstream source conflict").WithStatus(http.StatusConflict)
	ErrBindingChanged        = apperr.Define("llm.upstream_model_binding_changed", "upstream model binding changed; reload and retry").WithStatus(http.StatusConflict)
	ErrVendorExists          = apperr.Define("model_vendor.already_exists", "model vendor already exists").WithStatus(http.StatusConflict)
	ErrGroupExists           = apperr.Define("model_display_group.already_exists", "model display group already exists").WithStatus(http.StatusConflict)
	ErrVendorBuiltin         = apperr.Define("llm.model_vendor_builtin", "built-in model vendor cannot be deleted").WithStatus(http.StatusConflict)
	ErrVendorInUse           = apperr.Define("llm.model_vendor_in_use", "model vendor is in use").WithStatus(http.StatusConflict)
	ErrIconAssetInUse        = apperr.Define("llm.model_icon_asset_in_use", "model icon asset is in use").WithStatus(http.StatusConflict)
	ErrRemoteEmpty           = apperr.Define("llm.remote_models_empty_confirmation_required", "remote models snapshot is empty").WithStatus(http.StatusConflict)
	ErrRemoteSnapshotChanged = apperr.Define("llm.remote_models_snapshot_changed", "remote models snapshot changed").WithStatus(http.StatusConflict)
	ErrBreakerDisabled       = apperr.Define("llm.circuit_breaker_disabled", "circuit breaker is disabled").WithStatus(http.StatusConflict)

	ErrIconFileTooLarge    = apperr.Define("llm.model_icon_file_too_large", "model icon file too large").WithStatus(http.StatusRequestEntityTooLarge)
	ErrIconFileInvalid     = apperr.Define("llm.model_icon_file_invalid", "invalid model icon file").WithStatus(http.StatusBadRequest)
	ErrIconInvalid         = apperr.Define("llm.model_icon_invalid", "invalid model icon").WithStatus(http.StatusBadRequest)
	ErrIconAssetNotFound   = apperr.Define("llm.model_icon_asset_not_found", "model icon asset not found").WithStatus(http.StatusNotFound)
	ErrIconAssetUnavail    = apperr.Define("llm.model_icon_asset_unavailable", "model icon asset unavailable").WithStatus(http.StatusInternalServerError)
	ErrInvalidJSON         = apperr.Define("config.invalid_json", "invalid json config").WithStatus(http.StatusBadRequest)
	ErrInvalidHeaders      = apperr.Define("llm.invalid_headers_config", "invalid headers json config").WithStatus(http.StatusBadRequest)
	ErrInvalidAPIKeys      = apperr.Define("llm.invalid_api_keys_config", "invalid api keys config").WithStatus(http.StatusBadRequest)
	ErrInvalidDefaults     = apperr.Define("llm.invalid_protocol_defaults_config", "invalid protocol defaults config").WithStatus(http.StatusBadRequest)
	ErrInvalidAdapter      = apperr.Define("llm.invalid_adapter", "invalid adapter").WithStatus(http.StatusBadRequest)
	ErrInvalidCompatible   = apperr.Define("llm.invalid_compatible", "invalid compatible").WithStatus(http.StatusBadRequest)
	ErrInvalidBaseURL      = apperr.Define("request.invalid_upstream_base_url", "invalid upstream base url").WithStatus(http.StatusBadRequest)
	ErrInvalidKinds        = apperr.Define("llm.invalid_kinds", "invalid model kinds").WithStatus(http.StatusBadRequest)
	ErrInvalidPlatformName = apperr.Define("llm.invalid_platform_model_name", "invalid platform model name").WithStatus(http.StatusBadRequest)
	ErrInvalidAccessScope  = apperr.Define("request.invalid_model_access_scope", "invalid model access scope").WithStatus(http.StatusBadRequest)
	ErrSystemPromptLong    = apperr.Define("llm.system_prompt_too_long", "system prompt too long").WithStatus(http.StatusBadRequest)
	ErrInvalidModelOrder   = apperr.Define("request.invalid_model_order", "invalid model order").WithStatus(http.StatusBadRequest)
	ErrInvalidVendor       = apperr.Define("request.invalid_model_vendor", "invalid model vendor").WithStatus(http.StatusBadRequest)
	ErrInvalidDisplayGroup = apperr.Define("request.invalid_model_display_group", "invalid model display group").WithStatus(http.StatusBadRequest)
	ErrInvalidCapabilities = apperr.Define("llm.invalid_model_capabilities", "invalid model capability limits").WithStatus(http.StatusBadRequest)
	ErrReasoningFormat     = apperr.Define("llm.reasoning_invalid_format", "invalid reasoning capability format").WithStatus(http.StatusBadRequest)
	ErrReasoningLevels     = apperr.Define("llm.reasoning_invalid_levels", "invalid reasoning capability levels").WithStatus(http.StatusBadRequest)
	ErrReasoningDefault    = apperr.Define("llm.reasoning_invalid_default", "invalid reasoning capability default").WithStatus(http.StatusBadRequest)
	ErrReasoningBudgets    = apperr.Define("llm.reasoning_invalid_budgets", "invalid reasoning capability budgets").WithStatus(http.StatusBadRequest)
	ErrControlsInvalid     = apperr.Define("llm.model_controls_invalid", "invalid model controls").WithStatus(http.StatusBadRequest)
	ErrControlForbidden    = apperr.Define("llm.model_control_forbidden_path", "model control writes a forbidden path").WithStatus(http.StatusBadRequest)
	ErrProtocolRequired    = apperr.Define("llm.protocol_required", "protocol is required").WithStatus(http.StatusBadRequest)
	ErrInvalidCombination  = apperr.Define("llm.invalid_route_protocol_combination", "invalid route protocol combination").WithStatus(http.StatusBadRequest)
	ErrSourceUnavailable   = apperr.Define("llm.upstream_source_unavailable", "upstream source unavailable").WithStatus(http.StatusBadRequest)
	ErrNoActiveKey         = apperr.Define("llm.no_active_api_key", "no active api key").WithStatus(http.StatusBadRequest)
	ErrInvalidPermGroups   = apperr.Define("admin.invalid_permission_group_models", "invalid permission group models").WithStatus(http.StatusBadRequest)
	ErrRemoteUnavailable   = apperr.Define("llm.remote_models_unavailable", "remote models unavailable").WithStatus(http.StatusBadGateway)
	ErrCatalogFetchFailed  = apperr.Define("llm.model_catalog_fetch_failed", "failed to sync model catalog").WithStatus(http.StatusBadGateway)

	// Service-level errors that callers (conversation, billing, ...) inspect.
	ErrModelAccessDenied = apperr.Define("llm.model_access_denied", "you do not have access to this model").WithStatus(http.StatusForbidden)
	ErrRouteNotFound     = apperr.Define("route.not_found", "route not found").WithStatus(http.StatusNotFound)
	ErrRoutesUnavailable = apperr.Define("llm.routes_unavailable", "all model routes are unavailable").WithStatus(http.StatusServiceUnavailable)
	ErrUpstreamLimited   = apperr.Define("upstream.rate_limited", "upstream rate limited").WithStatus(http.StatusTooManyRequests)
)

// asStatus re-annotates a sentinel with a different HTTP status hint.
func asStatus(e *apperr.Error, status int) *apperr.Error { return e.WithStatus(status) }

// badRequest re-annotates a sentinel as a 400.
func badRequest(e *apperr.Error) *apperr.Error { return e.WithStatus(http.StatusBadRequest) }

// asBadRequest re-annotates a typed error as a 400 and passes other errors through.
func asBadRequest(err error) error {
	if te, ok := apperr.Find(err); ok {
		return te.WithStatus(http.StatusBadRequest)
	}
	return err
}
