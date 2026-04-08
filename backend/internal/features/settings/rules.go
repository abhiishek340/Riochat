package settings

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
)

// ---- authentication dependencies (spec 6.3.1) -------------------------------------

// authRules enforces the dependencies between sign-in switches. It runs when
// the batch contains an auth item, against the stored state overlaid with the
// batch, and may add items that switch dependent options off.
func (s *Service) authRules(ctx context.Context, st *patchState) error {
	const (
		fEmailLogin  = "auth:email_login_enabled"
		fEmailReg    = "auth:email_registration_enabled"
		fThirdParty  = "auth:third_party_login_enabled"
		fTurnstile   = "auth:turnstile_registration_enabled"
		fPasswordRst = "auth:password_reset_enabled"
	)

	// Locking everybody out: with neither username nor email login there must
	// be a way in through a third-party identity that an administrator owns.
	if !st.effBool("auth", "email_login_enabled") && !st.effBool("auth", "username_login_enabled") {
		if !st.effBool("auth", "third_party_login_enabled") {
			return dependency(ErrInvalidValue, fThirdParty, "username_or_email_login")
		}
		checker := s.port().SuperAdmins
		if checker == nil {
			return dependency(ErrInvalidValue, fThirdParty, "superadmin_identity")
		}
		ok, err := checker.HasBoundSuperAdmin(ctx)
		if err != nil {
			return fmt.Errorf("settings: super administrator check: %w", err)
		}
		if !ok {
			return dependency(ErrInvalidValue, fThirdParty, "superadmin_identity")
		}
	}

	if !st.effBool("auth", "email_login_enabled") {
		if st.explicitTrue("auth", "email_registration_enabled") {
			return dependency(ErrInvalidValue, fEmailReg, fEmailLogin)
		}
		st.set("auth", "email_registration_enabled", "false")
	}
	if !st.effBool("auth", "email_registration_enabled") {
		if st.explicitTrue("auth", "turnstile_registration_enabled") {
			return dependency(ErrInvalidValue, fTurnstile, fEmailReg)
		}
		st.set("auth", "turnstile_registration_enabled", "false")
	}
	if st.effBool("auth", "turnstile_registration_enabled") {
		for _, key := range []string{"turnstile_site_key", "turnstile_secret_key"} {
			if !st.present("auth", key) {
				return invalid(ErrInvalidValue, fieldID("auth", key), violation{"required_when", "auth:turnstile_registration_enabled=true"})
			}
		}
	}

	if !st.effBool("auth", "email_verification_enabled") {
		if st.explicitTrue("auth", "password_reset_enabled") {
			return dependency(ErrInvalidValue, fPasswordRst, "auth:email_verification_enabled")
		}
		st.set("auth", "password_reset_enabled", "false")
	} else {
		var missing []string
		for _, key := range []string{"smtp_host", "smtp_port", "smtp_username", "smtp_password"} {
			if !st.present("auth", key) {
				missing = append(missing, fieldID("auth", key))
			}
		}
		if len(missing) > 0 {
			return invalidFields(ErrSMTPInvalid, missing, violation{"required_together", "auth:email_verification_enabled=true"})
		}
		if v := intRange(1, 65535)(st.effective("auth", "smtp_port")); v != nil {
			return invalid(ErrSMTPInvalid, fieldID("auth", "smtp_port"), *v)
		}
	}

	if st.effBool("auth", "password_reset_enabled") &&
		!st.effBool("auth", "username_login_enabled") && !st.effBool("auth", "email_login_enabled") {
		return dependency(ErrInvalidValue, fPasswordRst, "username_or_email_login")
	}
	return nil
}

// ---- embedding (spec 6.3.2) ---------------------------------------------------------

// embeddingReady reports whether the effective embedding service can be used:
// enabled, with a model and an address.
func embeddingReady(st *patchState) bool {
	return st.effBool("file", "embedding_enabled") &&
		st.present("file", "rag_model") &&
		st.present("file", "embedding_host")
}

// embeddingRules cascades the switches that depend on the embedding service and
// then validates the combination.
func (s *Service) embeddingRules(ctx context.Context, st *patchState) error {
	fileKeys := []string{"embedding_enabled", "embedding_host", "embedding_protocol", "rag_model"}
	chatKeys := []string{"rag_enabled", "message_embedding_enabled", "semantic_context_enabled"}

	fileTouched := false
	for _, k := range fileKeys {
		if st.inBatch("file", k) {
			fileTouched = true
		}
	}
	chatTouched := false
	for _, k := range chatKeys {
		if st.inBatch("chat", k) {
			chatTouched = true
		}
	}
	if !fileTouched && !chatTouched {
		return nil
	}
	if fileTouched && !embeddingReady(st) {
		for _, k := range chatKeys {
			if !st.inBatch("chat", k) {
				st.set("chat", k, "false")
			}
		}
	}

	if st.effBool("chat", "semantic_context_enabled") && !st.effBool("chat", "message_embedding_enabled") {
		return dependency(ErrEmbeddingInvalid, "chat:message_embedding_enabled", "chat:semantic_context_enabled=true")
	}
	wanted := st.effBool("file", "embedding_enabled") || st.effBool("chat", "rag_enabled") ||
		st.effBool("chat", "message_embedding_enabled") || st.effBool("chat", "semantic_context_enabled")
	if !wanted {
		return nil
	}
	if !embeddingReady(st) {
		return dependency(ErrEmbeddingInvalid, "file:embedding_enabled", "embedding_service_ready")
	}
	if checker := s.port().VectorStore; checker != nil {
		ok, err := checker.VectorStoreAvailable(ctx)
		if err != nil {
			return fmt.Errorf("settings: vector store check: %w", err)
		}
		if !ok {
			return dependency(ErrEmbeddingInvalid, "file:embedding_enabled", "vector_store_available")
		}
	}
	return nil
}

// ---- extraction (spec 6.3.3) ---------------------------------------------------------

var ocrRequirements = map[string][]string{
	"tesseract": {"tesseract_ocr_base_url"},
	"rapidocr":  {"rapidocr_base_url"},
	"paddle":    {"paddle_ocr_base_url"},
	"tencent":   {"tencent_ocr_secret_id", "tencent_ocr_secret_key", "tencent_ocr_region"},
	"aliyun":    {"aliyun_ocr_access_key_id", "aliyun_ocr_access_key_secret", "aliyun_ocr_region"},
	"mistral":   {"mistral_ocr_base_url", "mistral_ocr_auth_token", "mistral_ocr_model"},
	"llm":       {"llm_ocr_base_url", "llm_ocr_model"},
}

var engineRequirements = map[string]string{
	"tika":    "tika_base_url",
	"docling": "docling_base_url",
	"mineru":  "mineru_base_url",
}
