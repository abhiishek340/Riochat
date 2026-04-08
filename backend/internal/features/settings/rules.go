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

func extractRules(st *patchState) error {
	engine := st.effective("extract", "engine")
	if key, ok := engineRequirements[engine]; ok && !st.present("extract", key) {
		return invalid(ErrExtractInvalid, fieldID("extract", key), violation{"required_when", "extract:engine=" + engine})
	}
	if st.effBool("extract", "image_ocr_enabled") || st.effBool("extract", "pdf_ocr_fallback_enabled") {
		ocr := st.effective("extract", "ocr_engine")
		for _, key := range ocrRequirements[ocr] {
			if !st.present("extract", key) {
				return invalid(ErrExtractInvalid, fieldID("extract", key), violation{"required_when", "extract:ocr_engine=" + ocr})
			}
		}
	}
	return nil
}

// ---- billing payments (spec 6.3.4) ------------------------------------------------------

func billingRules(st *patchState) error {
	providers, ok := parseProviders(st.effective("billing", "payment_providers"))
	if !ok {
		return invalid(ErrBillingPaymentInvalid, fieldID("billing", "payment_providers"), violation{"payment_provider", "stripe,epay"})
	}
	var missing []string
	add := func(key string) {
		f := fieldID("billing", key)
		for _, m := range missing {
			if m == f {
				return
			}
		}
		missing = append(missing, f)
	}
	for _, p := range providers {
		switch p {
		case "stripe":
			for _, key := range []string{"stripe_secret_key", "stripe_webhook_secret"} {
				if !st.present("billing", key) {
					add(key)
				}
			}
		case "epay":
			if floatRange(0.000001, 1000)(st.effective("billing", "usd_to_cny_rate")) != nil {
				add("usd_to_cny_rate")
			}
			if g := st.effective("billing", "epay_gateway_url"); g == "" || !validEpayGateway(g) {
				add("epay_gateway_url")
			}
			if t := st.effective("billing", "epay_types"); t == "" || epayTypesCheck()(t) != nil {
				add("epay_types")
			}
			for _, key := range []string{"epay_pid", "epay_key"} {
				if !st.present("billing", key) {
					add(key)
				}
			}
		}
	}
	if len(missing) > 0 {
		return invalidFields(ErrBillingPaymentInvalid, missing, violation{"required_together", "billing:payment_providers"})
	}
	return nil
}

// ---- embedding space signature (spec 7) ----------------------------------------------------

// ModelSignature identifies vectors by model and dimension: the first four
// bytes of SHA-256("<model>@<dimensions>") in hex, then "@<dimensions>".
func ModelSignature(model string, dims int) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s@%d", model, dims)))
	return hex.EncodeToString(sum[:4]) + "@" + strconv.Itoa(dims)
}

// SpaceSignature identifies vectors by model, dimension and provider endpoint:
// the first eight bytes of SHA-256("<model>@<dimensions>@<endpoint>") in hex,
// then "@<dimensions>". The endpoint has surrounding space and trailing slashes
// removed.
func SpaceSignature(model string, dims int, endpoint string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s@%d@%s", model, dims, normalizeHost(endpoint))))
	return hex.EncodeToString(sum[:8]) + "@" + strconv.Itoa(dims)
}

func normalizeHost(h string) string { return strings.TrimRight(strings.TrimSpace(h), "/") }

// deriveSignature keeps file/embedding_model_signature server-owned. It runs
// for batches that touch the file namespace and reports whether the index must
// be reconciled afterwards.
func (s *Service) deriveSignature(st *patchState) (reconcile bool) {
	rt := s.Runtime()
	curModel := strings.TrimSpace(rt.File.RAGModel)
	curDims := rt.File.EmbeddingOutputDimensions
	curHost := rt.File.EmbeddingHost

	model, dims, host := curModel, curDims, curHost
	for _, o := range st.ops {
		if o.ns() != "file" {
			continue
		}
		switch o.key() {
		case "rag_model":
			model = strings.TrimSpace(o.value)
		case "embedding_output_dimensions":
			if n, ok := parseInt(strings.TrimSpace(o.value)); ok {
				dims = int(n)
			}
		case "embedding_host":
			host = o.value
		}
	}
	touchedSpace := st.inBatch("file", "rag_model") || st.inBatch("file", "embedding_output_dimensions") ||
		st.inBatch("file", "embedding_host")

	changed := model != curModel || dims != curDims || normalizeHost(host) != normalizeHost(curHost)
	switch {
	case changed:
		sig := ""
		if model != "" {
			sig = SpaceSignature(model, dims, host)
		}
		st.set("file", "embedding_model_signature", sig)
	case strings.TrimSpace(rt.File.EmbeddingModelSignature) == "" && model != "":
		st.set("file", "embedding_model_signature", ModelSignature(model, dims))
		return true
	case st.inBatch("file", "embedding_model_signature"):
		st.set("file", "embedding_model_signature", rt.File.EmbeddingModelSignature)
	}
	return touchedSpace
}

// backfillSignature writes the legacy-compatible signature when the runtime has
// none although a model is configured (spec 5.5). Failures are ignored.
func (s *Service) backfillSignature(ctx context.Context) {
	s.writeMu.Lock()
	defer s.writeMu.Unlock()
	rt := s.Runtime()
	model := strings.TrimSpace(rt.File.RAGModel)
	if strings.TrimSpace(rt.File.EmbeddingModelSignature) != "" || model == "" {
		return
	}
	sig := ModelSignature(model, rt.File.EmbeddingOutputDimensions)
	d := reg.lookup("file", "embedding_model_signature")
	if err := s.repo.upsertValue(ctx, d.ns, d.key, sig, string(d.typ), d.desc, s.clock.Now()); err != nil {
		return
	}
	s.cacheDelete(ctx, d.ns, d.key)
	_ = s.reloadLocked(ctx)
}

// ---- audit -------------------------------------------------------------------------------

const auditResource = "system_settings"

// auditPatch records the submitted items with secrets redacted.
func (s *Service) auditPatch(ctx context.Context, actor Actor, items []PatchItem) {
	w := s.port().Audit
	if w == nil {
		return
	}
	detail := make([]map[string]any, 0, len(items))
	for _, it := range items {
		ns, key := strings.TrimSpace(it.Namespace), strings.TrimSpace(it.Key)
		value := it.Value
		if d := reg.lookup(ns, key); d != nil && d.sensitive && strings.TrimSpace(value) != "" {
			value = redacted
		}
		detail = append(detail, map[string]any{"namespace": ns, "key": key, "value": value, "clear": it.Clear})
	}
	s.record(ctx, actor, "settings.update", map[string]any{"items": detail})
}

// redacted replaces a secret in audit details.
const redacted = "[REDACTED]"

func (s *Service) record(ctx context.Context, actor Actor, action string, detail map[string]any) {
	w := s.port().Audit
	if w == nil {
		return
	}
	w.Record(ctx, AuditEntry{
		ActorUserID: actor.UserID, RequestID: actor.RequestID, ClientIP: actor.ClientIP, UserAgent: actor.UserAgent,
		Action: action, Resource: auditResource, Detail: detail,
	})
}
