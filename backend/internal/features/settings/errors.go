package settings

import (
	"errors"
	"net/http"
	"sort"

	"github.com/riochat/riochat/backend/internal/platform/apperr"
)

// Error codes of the settings area. These are API contracts.
var (
	ErrInvalidNamespace         = apperr.Define("settings.invalid_namespace", "invalid setting namespace")
	ErrInvalidKey               = apperr.Define("settings.invalid_key", "invalid setting key")
	ErrInvalidValue             = apperr.Define("settings.invalid_value", "invalid setting value")
	ErrSMTPInvalid              = apperr.Define("settings.smtp_invalid", "invalid SMTP settings")
	ErrBillingPaymentInvalid    = apperr.Define("settings.billing_payment_invalid", "invalid billing payment settings")
	ErrEmbeddingInvalid         = apperr.Define("settings.embedding_invalid", "invalid embedding settings")
	ErrExtractInvalid           = apperr.Define("settings.extract_invalid", "invalid file extraction settings")
	ErrModelOptionPolicyInvalid = apperr.Define("settings.model_option_policy_invalid", "invalid model option policy settings")

	ErrUserUnknownKey   = apperr.Define("user_settings.unknown_key", "unknown setting key")
	ErrUserInvalidValue = apperr.Define("user_settings.invalid_value", "invalid user setting value")

	ErrEmbeddingServiceUnavailable = apperr.Define("embedding.service_unavailable", "embedding service is not available")
	ErrEmbeddingNotConfigured      = apperr.Define("embedding.service_not_configured", "embedding service is not configured")
	ErrTooManyFiles                = apperr.Define("embedding.too_many_files", "too many files for embedding")

	errTikaRuntime      = apperr.Define("runtime.tika_unavailable", "tika runtime service unavailable")
	errDoclingRuntime   = apperr.Define("runtime.docling_unavailable", "docling runtime service unavailable")
	errTesseractRuntime = apperr.Define("runtime.tesseract_unavailable", "tesseract runtime service unavailable")
	errRapidOCRRuntime  = apperr.Define("runtime.rapidocr_unavailable", "rapidocr runtime service unavailable")
	errMinerURuntime    = apperr.Define("runtime.mineru_unavailable", "mineru runtime service unavailable")
)

// Sentinels that an embedding index port reports; the handlers translate them.
var (
	// ErrReindexRunning means a rebuild is already in progress.
	ErrReindexRunning = errors.New("settings: embedding reindex already running")
	// ErrNothingConfigured is returned by a port when embedding is not set up.
	ErrNothingConfigured = errors.New("settings: embedding is not configured")
	// ErrPortUnavailable is returned by a port that cannot serve right now.
	ErrPortUnavailable = errors.New("settings: embedding service unavailable")
)

// codeGroup selects which public error code a value-level failure uses.
type codeGroup int

const (
	groupValue codeGroup = iota
	groupSMTP
	groupBilling
	groupEmbedding
	groupExtract
	groupModelOption
)

func (g codeGroup) err() *apperr.Error {
	switch g {
	case groupSMTP:
		return ErrSMTPInvalid
	case groupBilling:
		return ErrBillingPaymentInvalid
	case groupEmbedding:
		return ErrEmbeddingInvalid
	case groupExtract:
		return ErrExtractInvalid
	case groupModelOption:
		return ErrModelOptionPolicyInvalid
	}
	return ErrInvalidValue
}

// violation is one failed validation rule: a stable machine token and an
// optional parameter. It never carries the submitted value.
type violation struct {
	rule  string
	param string
}

// fieldID renders the "namespace:key" form used in error details.
func fieldID(ns, key string) string { return ns + ":" + key }

// invalid builds the 400 error for one failing field.
func invalid(base *apperr.Error, field string, v violation) error {
	d := map[string]any{"rule": v.rule}
	if field != "" {
		d["field"] = field
	}
	if v.param != "" {
		d["param"] = v.param
	}
	return base.WithDetails(d).WithStatus(http.StatusBadRequest)
}

// invalidFields builds the 400 error for a group of failing fields.
func invalidFields(base *apperr.Error, fields []string, v violation) error {
	d := map[string]any{"rule": v.rule, "fields": fields}
	if v.param != "" {
		d["param"] = v.param
	}
	return base.WithDetails(d).WithStatus(http.StatusBadRequest)
}

// dependency builds a dependency failure on field.
func dependency(base *apperr.Error, field, param string) error {
	return invalid(base, field, violation{"dependency", param})
}

// featureLocked builds the 404 error for a patch that touches locked keys.
func featureLocked(feature string, keys []string) error {
	sorted := append([]string(nil), keys...)
	sort.Strings(sorted)
	return apperr.ErrFeatureDisabled.
		WithDetails(map[string]any{"feature": feature, "keys": sorted}).
		WithStatus(http.StatusNotFound)
}
