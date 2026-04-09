package settings_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/riochat/riochat/backend/internal/features/settings"
	"github.com/riochat/riochat/backend/internal/platform/config"
	"github.com/riochat/riochat/backend/internal/testkit"
)

// ---- authentication dependencies --------------------------------------------------------

func TestDisablingEmailLoginSwitchesDependentsOff(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.advance(time.Minute)
	h.mustPatch(
		set("auth", "turnstile_site_key", "site"), set("auth", "turnstile_secret_key", "secret"),
		set("auth", "turnstile_registration_enabled", "true"),
	)
	if h.val("auth", "turnstile_registration_enabled") != "true" || h.val("auth", "email_registration_enabled") != "true" {
		t.Fatal("precondition")
	}
	h.mustPatch(set("auth", "email_login_enabled", "false"))
	if h.val("auth", "email_registration_enabled") != "false" || h.val("auth", "turnstile_registration_enabled") != "false" {
		t.Fatalf("dependents not switched off: reg=%s turnstile=%s",
			h.val("auth", "email_registration_enabled"), h.val("auth", "turnstile_registration_enabled"))
	}
	rt := h.svc.Runtime().Auth
	if rt.EmailRegistrationEnabled || rt.TurnstileRegistrationEnabled || rt.EmailLoginEnabled {
		t.Fatalf("runtime: %+v", rt)
	}
}

func TestExplicitRegistrationWithoutEmailLoginIsRejected(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	w := h.patch(set("auth", "email_login_enabled", "false"), set("auth", "email_registration_enabled", "true"))
	h.expectInvalid(w, "settings.invalid_value", "auth:email_registration_enabled", "dependency", "auth:email_login_enabled")
	if h.val("auth", "email_login_enabled") != "true" {
		t.Fatal("nothing may be applied")
	}
	// explicit false is fine
	h.mustPatch(set("auth", "email_login_enabled", "false"), set("auth", "email_registration_enabled", "false"))
	// and turning registration on later, while email login stays off, is rejected
	w = h.patch(set("auth", "email_registration_enabled", "true"))
	h.expectInvalid(w, "settings.invalid_value", "auth:email_registration_enabled", "dependency", "auth:email_login_enabled")
}

func TestTurnstileRequiresRegistration(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.mustPatch(set("auth", "turnstile_site_key", "s"), set("auth", "turnstile_secret_key", "k"))
	h.mustPatch(set("auth", "email_registration_enabled", "false"))
	w := h.patch(set("auth", "turnstile_registration_enabled", "true"))
	h.expectInvalid(w, "settings.invalid_value", "auth:turnstile_registration_enabled", "dependency", "auth:email_registration_enabled")
}

func TestTurnstileRequiresKeys(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	w := h.patch(set("auth", "turnstile_registration_enabled", "true"))
	h.expectInvalid(w, "settings.invalid_value", "auth:turnstile_site_key", "required_when", "auth:turnstile_registration_enabled=true")
	w = h.patch(set("auth", "turnstile_registration_enabled", "true"), set("auth", "turnstile_site_key", "site"))
	h.expectInvalid(w, "settings.invalid_value", "auth:turnstile_secret_key", "required_when", "auth:turnstile_registration_enabled=true")
	h.mustPatch(set("auth", "turnstile_registration_enabled", "true"), set("auth", "turnstile_site_key", "site"), set("auth", "turnstile_secret_key", "secret"))
	// an already stored secret counts
	h.mustPatch(set("auth", "turnstile_registration_enabled", "false"))
	h.mustPatch(set("auth", "turnstile_registration_enabled", "true"))
	// clearing the secret while turnstile is on is rejected
	w = h.patch(clearItem("auth", "turnstile_secret_key"))
	h.expectInvalid(w, "settings.invalid_value", "auth:turnstile_secret_key", "required_when", "-")
}

func TestLockoutProtection(t *testing.T) {
	both := []map[string]any{set("auth", "email_login_enabled", "false"), set("auth", "username_login_enabled", "false")}

	h := newHarness(t, settings.Ports{})
	w := h.patch(both...)
	h.expectInvalid(w, "settings.invalid_value", "auth:third_party_login_enabled", "dependency", "superadmin_identity")

	h = newHarness(t, settings.Ports{SuperAdmins: fakeAdmins{ok: false}})
	h.expectInvalid(h.patch(both...), "settings.invalid_value", "auth:third_party_login_enabled", "dependency", "superadmin_identity")

	h = newHarness(t, settings.Ports{SuperAdmins: fakeAdmins{ok: true}})
	h.expectInvalid(h.patch(append(both, set("auth", "third_party_login_enabled", "false"))...),
		"settings.invalid_value", "auth:third_party_login_enabled", "dependency", "username_or_email_login")
	h.mustPatch(both...)
	rt := h.svc.Runtime().Auth
	if rt.EmailLoginEnabled || rt.UsernameLoginEnabled {
		t.Fatalf("runtime: %+v", rt)
	}

	h = newHarness(t, settings.Ports{SuperAdmins: fakeAdmins{err: errors.New("db down")}})
	w = h.patch(both...)
	testkit.Expect(t, w, 500, "internal.error")

	// unrelated auth patches do not consult the checker while a login method stays on
	h = newHarness(t, settings.Ports{SuperAdmins: fakeAdmins{err: errors.New("db down")}})
	h.mustPatch(set("auth", "token_ttl_hours", "30"))
}

func TestEmailVerificationRequiresSMTP(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	w := h.patch(set("auth", "email_verification_enabled", "true"))
	testkit.Expect(t, w, 400, "settings.smtp_invalid")
	d := h.details(w)
	if d["rule"] != "required_together" || d["param"] != "auth:email_verification_enabled=true" {
		t.Fatalf("details: %v", d)
	}
	got := strings.Join(fieldsOf(d), ",")
	if got != "auth:smtp_host,auth:smtp_username,auth:smtp_password" {
		t.Fatalf("missing fields: %s", got)
	}

	w = h.patch(set("auth", "email_verification_enabled", "true"), set("auth", "smtp_host", "mail.example.com"), set("auth", "smtp_username", "bot"))
	testkit.Expect(t, w, 400, "settings.smtp_invalid")
	if got := strings.Join(fieldsOf(h.details(w)), ","); got != "auth:smtp_password" {
		t.Fatalf("missing fields: %s", got)
	}

	h.mustPatch(set("auth", "email_verification_enabled", "true"), set("auth", "smtp_host", "mail.example.com"),
		set("auth", "smtp_username", "bot"), set("auth", "smtp_password", "pw"))
	// clearing a required secret while verification is on is rejected
	w = h.patch(clearItem("auth", "smtp_password"))
	testkit.Expect(t, w, 400, "settings.smtp_invalid")
}

func TestPasswordResetDependencies(t *testing.T) {
	h := newHarness(t, settings.Ports{SuperAdmins: fakeAdmins{ok: true}})
	w := h.patch(set("auth", "password_reset_enabled", "true"))
	h.expectInvalid(w, "settings.invalid_value", "auth:password_reset_enabled", "dependency", "auth:email_verification_enabled")

	h.mustPatch(set("auth", "email_verification_enabled", "true"), set("auth", "smtp_host", "m"), set("auth", "smtp_username", "u"),
		set("auth", "smtp_password", "p"), set("auth", "password_reset_enabled", "true"))
	if !h.svc.Runtime().Auth.PasswordResetEnabled {
		t.Fatal("password reset should be on")
	}
	// no way to sign in with a password any more
	w = h.patch(set("auth", "email_login_enabled", "false"), set("auth", "username_login_enabled", "false"))
	h.expectInvalid(w, "settings.invalid_value", "auth:password_reset_enabled", "dependency", "username_or_email_login")

	// turning verification off switches reset off silently
	h.mustPatch(set("auth", "email_verification_enabled", "false"))
	if h.val("auth", "password_reset_enabled") != "false" {
		t.Fatal("reset should be forced off")
	}
}

// ---- embedding ------------------------------------------------------------------------------

func enableEmbedding() []map[string]any {
	return []map[string]any{
		set("file", "embedding_enabled", "true"), set("file", "embedding_host", "http://emb.internal:8080"),
		set("chat", "rag_enabled", "true"), set("chat", "message_embedding_enabled", "true"), set("chat", "semantic_context_enabled", "true"),
	}
}

func TestEmbeddingSwitchesCascadeOff(t *testing.T) {
	emb := &fakeEmbedding{}
	h := newHarness(t, settings.Ports{Embedding: emb})
	h.mustPatch(enableEmbedding()...)
	rt := h.svc.Runtime()
	if !rt.Chat.RAGEnabled || !rt.Chat.MessageEmbeddingEnabled || !rt.Chat.SemanticContextEnabled || !rt.File.EmbeddingEnabled {
		t.Fatalf("precondition: %+v", rt.Chat)
	}
	h.mustPatch(set("file", "embedding_enabled", "false"))
	for _, k := range []string{"rag_enabled", "message_embedding_enabled", "semantic_context_enabled"} {
		if h.val("chat", k) != "false" {
			t.Errorf("chat/%s should be false", k)
		}
	}
	// emptying the host while the service stays enabled is not allowed ...
	h.mustPatch(enableEmbedding()...)
	h.expectInvalid(h.patch(set("file", "embedding_host", "")), "settings.embedding_invalid", "file:embedding_enabled", "dependency", "embedding_service_ready")
	// ... but together with switching it off the dependents follow
	h.mustPatch(set("file", "embedding_host", ""), set("file", "embedding_enabled", "false"))
	if h.val("chat", "rag_enabled") != "false" || h.val("chat", "semantic_context_enabled") != "false" {
		t.Fatal("dependents should be switched off")
	}
	// an explicit value in the same batch is not overridden (and is then rejected)
	w := h.patch(set("file", "embedding_enabled", "false"), set("chat", "rag_enabled", "true"))
	h.expectInvalid(w, "settings.embedding_invalid", "file:embedding_enabled", "dependency", "embedding_service_ready")
}

func TestEmbeddingReadinessRules(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.expectInvalid(h.patch(set("chat", "rag_enabled", "true")), "settings.embedding_invalid", "file:embedding_enabled", "dependency", "embedding_service_ready")
	h.expectInvalid(h.patch(set("chat", "message_embedding_enabled", "true")), "settings.embedding_invalid", "file:embedding_enabled", "dependency", "embedding_service_ready")
	// enabled without an address is not ready
	h.expectInvalid(h.patch(set("file", "embedding_enabled", "true")), "settings.embedding_invalid", "file:embedding_enabled", "dependency", "embedding_service_ready")
	// a blank model is not ready either
	w := h.patch(set("file", "embedding_enabled", "true"), set("file", "embedding_host", "http://e"), set("file", "rag_model", " "))
	h.expectInvalid(w, "settings.embedding_invalid", "file:embedding_enabled", "dependency", "embedding_service_ready")

	h.expectInvalid(h.patch(append(enableEmbedding()[:2], set("chat", "semantic_context_enabled", "true"), set("chat", "message_embedding_enabled", "false"))...),
		"settings.embedding_invalid", "chat:message_embedding_enabled", "dependency", "chat:semantic_context_enabled=true")

	h.mustPatch(set("file", "embedding_enabled", "true"), set("file", "embedding_host", "http://e"))
	if !h.svc.Runtime().File.EmbeddingEnabled {
		t.Fatal("embedding should be on")
	}
}

func TestVectorStoreRule(t *testing.T) {
	h := newHarness(t, settings.Ports{VectorStore: fakeVectors{ok: false}})
	h.expectInvalid(h.patch(enableEmbedding()[:2]...), "settings.embedding_invalid", "file:embedding_enabled", "dependency", "vector_store_available")
	// turning things off does not need the store
	h.mustPatch(set("file", "embedding_enabled", "false"))

	h = newHarness(t, settings.Ports{VectorStore: fakeVectors{ok: true}})
	h.mustPatch(enableEmbedding()...)

	h = newHarness(t, settings.Ports{VectorStore: fakeVectors{err: errors.New("probe failed")}})
	testkit.Expect(t, h.patch(enableEmbedding()[:2]...), 500, "internal.error")
}

func TestEmbeddingSpaceSignature(t *testing.T) {
	emb := &fakeEmbedding{}
	h := newHarness(t, settings.Ports{Embedding: emb})

	defModel := settings.RegistryDefault("file", "rag_model")
	if got, want := h.val("file", "embedding_model_signature"), settings.ModelSignature(defModel, 1536); got != want {
		t.Fatalf("start-up backfill: %q want %q", got, want)
	}
	if h.svc.Runtime().File.EmbeddingModelSignature == "" {
		t.Fatal("runtime must carry the signature")
	}

	h.mustPatch(set("file", "rag_model", "text-embedding-3-small"))
	want := settings.SpaceSignature("text-embedding-3-small", 1536, "")
	if got := h.val("file", "embedding_model_signature"); got != want {
		t.Fatalf("new space signature: %q want %q", got, want)
	}
	if emb.reconciles() != 1 {
		t.Fatalf("reconcile calls: %d", emb.reconciles())
	}

	// A client-supplied signature is ignored when the space is unchanged.
	h.mustPatch(set("file", "embedding_model_signature", "forged"), set("file", "embedding_timeout_seconds", "30"))
	if got := h.val("file", "embedding_model_signature"); got != want {
		t.Fatalf("client signature must be ignored: %q", got)
	}
	if emb.reconciles() != 1 {
		t.Fatal("no space change, no reconcile")
	}

	// ... and when the space changes the server value wins as well.
	h.mustPatch(set("file", "embedding_model_signature", "forged"), set("file", "embedding_output_dimensions", "768"))
	want2 := settings.SpaceSignature("text-embedding-3-small", 768, "")
	if got := h.val("file", "embedding_model_signature"); got != want2 || want2 == want {
		t.Fatalf("dimension change: %q want %q", got, want2)
	}
	if emb.reconciles() != 2 {
		t.Fatalf("reconcile calls: %d", emb.reconciles())
	}

	// Host change (trailing slash and spaces ignored for comparison).
	h.mustPatch(set("file", "embedding_host", "http://emb.example.com/"))
	want3 := settings.SpaceSignature("text-embedding-3-small", 768, "http://emb.example.com")
	if got := h.val("file", "embedding_model_signature"); got != want3 {
		t.Fatalf("host change: %q want %q", got, want3)
	}
	if emb.reconciles() != 3 {
		t.Fatalf("reconcile calls: %d", emb.reconciles())
	}
	h.mustPatch(set("file", "embedding_host", " http://emb.example.com// "))
	if h.val("file", "embedding_model_signature") != want3 {
		t.Fatal("cosmetic host change must keep the signature")
	}
	// touching a space key with the same value still reconciles (idempotent retry)
	if emb.reconciles() != 4 {
		t.Fatalf("touching a space key reconciles: %d", emb.reconciles())
	}
	// Unrelated file keys do not reconcile.
	h.mustPatch(set("file", "rag_top_k", "8"))
	if emb.reconciles() != 4 {
		t.Fatal("unrelated key must not reconcile")
	}
}

func TestSignatureBackfillOnPatch(t *testing.T) {
	emb := &fakeEmbedding{}
	h := newHarness(t, settings.Ports{Embedding: emb})
	h.setRaw("file", "embedding_model_signature", "")
	if err := h.svc.Reload(ctxBG()); err != nil {
		t.Fatal(err)
	}
	h.mustPatch(set("file", "rag_top_k", "7"))
	want := settings.ModelSignature(settings.RegistryDefault("file", "rag_model"), 1536)
	if got := h.val("file", "embedding_model_signature"); got != want {
		t.Fatalf("legacy backfill: %q want %q", got, want)
	}
	if emb.reconciles() != 1 {
		t.Fatalf("backfill reconciles: %d", emb.reconciles())
	}
}

func TestReconcileFailureKeepsSettings(t *testing.T) {
	emb := &fakeEmbedding{reconcileErr: errBoom}
	h := newHarness(t, settings.Ports{Embedding: emb})
	w := h.patch(set("file", "rag_model", "other-model"))
	testkit.Expect(t, w, 500, "internal.error")
	if h.val("file", "rag_model") != "other-model" {
		t.Fatal("settings are already saved")
	}
	// retrying the same patch succeeds once reconcile works
	emb.reconcileErr = nil
	h.mustPatch(set("file", "rag_model", "other-model"))
	if emb.reconciles() != 2 {
		t.Fatalf("reconcile calls: %d", emb.reconciles())
	}
}

func TestStartupBackfillUsesRuntimeModel(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.setRaw("file", "embedding_model_signature", "")
	h.setRaw("file", "rag_model", "my-model")
	h.setRaw("file", "embedding_output_dimensions", "256")
	if err := h.svc.Reload(ctxBG()); err != nil {
		t.Fatal(err)
	}
	h.svc.Backfill(ctxBG())
	if got, want := h.val("file", "embedding_model_signature"), settings.ModelSignature("my-model", 256); got != want {
		t.Fatalf("%q want %q", got, want)
	}
	if h.svc.Runtime().File.EmbeddingModelSignature != h.val("file", "embedding_model_signature") {
		t.Fatal("runtime re-applied after backfill")
	}
	// no model: nothing written
	h.setRaw("file", "embedding_model_signature", "")
	h.setRaw("file", "rag_model", "")
	_ = h.svc.Reload(ctxBG())
	h.svc.Backfill(ctxBG())
	if h.stored("file", "embedding_model_signature") != "" {
		t.Fatal("no model, no signature")
	}
}

// ---- extraction ---------------------------------------------------------------------------------

func TestExtractEngineRequirements(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	for _, c := range []struct{ engine, key string }{{"tika", "tika_base_url"}, {"docling", "docling_base_url"}, {"mineru", "mineru_base_url"}} {
		w := h.patch(set("extract", "engine", c.engine), set("extract", c.key, ""))
		h.expectInvalid(w, "settings.extract_invalid", "extract:"+c.key, "required_when", "extract:engine="+c.engine)
	}
	h.mustPatch(set("extract", "engine", "tika"))
	// emptying the address of the selected engine later is rejected too
	h.expectInvalid(h.patch(set("extract", "tika_base_url", " ")), "settings.extract_invalid", "extract:tika_base_url", "required_when", "extract:engine=tika")
	h.mustPatch(set("extract", "engine", "builtin"), set("extract", "tika_base_url", ""))
	// the rules also run when only the file namespace is touched
	h.setRaw("extract", "engine", "docling")
	h.setRaw("extract", "docling_base_url", "")
	w := h.patch(set("file", "rag_top_k", "9"))
	testkit.Expect(t, w, 400, "settings.extract_invalid")
}

func TestOCREngineRequirements(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	// OCR switches off: no engine requirement
	h.mustPatch(set("extract", "ocr_engine", "mistral"))

	w := h.patch(set("extract", "image_ocr_enabled", "true"))
	h.expectInvalid(w, "settings.extract_invalid", "extract:mistral_ocr_auth_token", "required_when", "extract:ocr_engine=mistral")
	h.mustPatch(set("extract", "image_ocr_enabled", "true"), set("extract", "mistral_ocr_auth_token", "tok"))

	h.mustPatch(set("extract", "image_ocr_enabled", "false"))
	cases := []struct{ engine, missing string }{
		{"paddle", "paddle_ocr_base_url"},
		{"tencent", "tencent_ocr_secret_id"},
		{"aliyun", "aliyun_ocr_access_key_id"},
		{"llm", "llm_ocr_base_url"},
	}
	for _, c := range cases {
		w := h.patch(set("extract", "pdf_ocr_fallback_enabled", "true"), set("extract", "ocr_engine", c.engine))
		h.expectInvalid(w, "settings.extract_invalid", "extract:"+c.missing, "required_when", "extract:ocr_engine="+c.engine)
	}
	// engines with defaults pass
	h.mustPatch(set("extract", "pdf_ocr_fallback_enabled", "true"), set("extract", "ocr_engine", "tesseract"))
	h.mustPatch(set("extract", "ocr_engine", "rapidocr"))
	// a stored sensitive value counts as present
	h.mustPatch(set("extract", "ocr_engine", "tencent"), set("extract", "tencent_ocr_secret_id", "id"), set("extract", "tencent_ocr_secret_key", "key"))
	h.mustPatch(set("extract", "ocr_engine", "rapidocr"))
	h.mustPatch(set("extract", "ocr_engine", "tencent"))
}

// ---- billing ----------------------------------------------------------------------------------------

func TestBillingPaymentRequirements(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	w := h.patch(set("billing", "payment_providers", "stripe"))
	testkit.Expect(t, w, 400, "settings.billing_payment_invalid")
	d := h.details(w)
	if d["rule"] != "required_together" || d["param"] != "billing:payment_providers" {
		t.Fatalf("details: %v", d)
	}
	if got := strings.Join(fieldsOf(d), ","); got != "billing:stripe_secret_key,billing:stripe_webhook_secret" {
		t.Fatalf("fields: %s", got)
	}
	h.mustPatch(set("billing", "payment_providers", "stripe"), set("billing", "stripe_secret_key", "sk"), set("billing", "stripe_webhook_secret", "whsec"))
	// now stored; later patches of the namespace keep passing
	h.mustPatch(set("billing", "display_currency", "CNY"))
	// removing a required secret while stripe is on is rejected
	w = h.patch(clearItem("billing", "stripe_webhook_secret"))
	testkit.Expect(t, w, 400, "settings.billing_payment_invalid")

	w = h.patch(set("billing", "payment_providers", "stripe,epay"))
	testkit.Expect(t, w, 400, "settings.billing_payment_invalid")
	if got := strings.Join(fieldsOf(h.details(w)), ","); got != "billing:epay_gateway_url,billing:epay_pid,billing:epay_key" {
		t.Fatalf("epay fields: %s", got)
	}
	h.mustPatch(set("billing", "payment_providers", "Stripe, EPAY, disabled"), set("billing", "epay_gateway_url", "https://pay.example.com"),
		set("billing", "epay_pid", "1001"), set("billing", "epay_key", "k"))
	h.mustPatch(set("billing", "payment_providers", "disabled"))
	h.mustPatch(set("billing", "payment_providers", ""))
}

func TestDuplicateEpayTypesAndCount(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	dup := `[{"name":"A","type":"x"},{"name":"B","type":"x"}]`
	h.expectInvalid(h.patch(set("billing", "epay_types", dup)), "settings.billing_payment_invalid", "billing:epay_types", "payment_type_unique", "-")
	var b strings.Builder
	b.WriteString("[")
	for i := 0; i < 11; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(`{"name":"n","type":"t` + string(rune('a'+i)) + `"}`)
	}
	b.WriteString("]")
	h.expectInvalid(h.patch(set("billing", "epay_types", b.String())), "settings.billing_payment_invalid", "billing:epay_types", "payment_count", "1,10")
}

// ---- locks -------------------------------------------------------------------------------------------

func TestFeatureLocksInLocalMode(t *testing.T) {
	h := newHarness(t, settings.Ports{}, localMode())
	w := h.patch(set("billing", "mode", "usage"))
	testkit.Expect(t, w, 404, "feature.disabled")
	d := h.details(w)
	keys, _ := d["keys"].([]any)
	if d["feature"] != "billingGating" || len(keys) != 1 || keys[0] != "billing.mode" {
		t.Fatalf("details: %v", d)
	}
	if testkit.Envelope(t, w).ErrorMsg != "this feature is not available on this server" {
		t.Fatalf("message: %s", w.Body)
	}

	// Other keys in the same batch do not help and are not applied.
	w = h.patch(set("billing", "display_currency", "CNY"), set("billing", "mode", "period"), set("billing", "payment_providers", "stripe"))
	testkit.Expect(t, w, 404, "feature.disabled")
	keys, _ = h.details(w)["keys"].([]any)
	if len(keys) != 2 || keys[0] != "billing.mode" || keys[1] != "billing.payment_providers" {
		t.Fatalf("keys must be sorted dotted names: %v", keys)
	}
	if h.val("billing", "display_currency") != "USD" {
		t.Fatal("no partial application")
	}

	// Resubmitting the stored value is allowed, together with other billing keys.
	h.mustPatch(set("billing", "mode", " self "), set("billing", "display_currency", "CNY"), set("billing", "payment_providers", "disabled"))
	if h.val("billing", "display_currency") != "CNY" {
		t.Fatal("unlocked billing keys stay writable")
	}

	w = h.patch(set("desktop", "download_url", "https://example.com/other"))
	testkit.Expect(t, w, 404, "feature.disabled")
	if h.details(w)["feature"] != "desktopDownload" {
		t.Fatalf("details: %v", h.details(w))
	}
	testkit.Expect(t, h.patch(set("desktop", "download_enabled", "false")), 404, "feature.disabled")
	h.mustPatch(set("desktop", "download_enabled", "true"), set("desktop", "download_url", settings.RegistryDefault("desktop", "download_url")))

	// A locked key that is invalid is reported as invalid first.
	h.expectInvalid(h.patch(set("billing", "mode", "bogus")), "settings.invalid_value", "billing:mode", "enum", "-")
}

func TestNoLocksInServerMode(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.mustPatch(set("billing", "mode", "usage"), set("desktop", "download_url", "https://example.com/other"), set("desktop", "download_enabled", "false"))
	if h.val("billing", "mode") != "usage" || h.val("desktop", "download_url") != "https://example.com/other" {
		t.Fatal("server mode applies locked keys")
	}
}

// ---- runtime -----------------------------------------------------------------------------------------

func TestRuntimeNormalizationFromStoredValues(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	ctx := context.Background()
	h.setRaw("chat", "context_compact_trigger_percent", "5")
	h.setRaw("mcp", "mcp_tool_timeout_seconds", "0")
	h.setRaw("mcp", "mcp_max_selected_tools_per_message", "500")
	h.setRaw("auth", "token_ttl_hours", "0")
	h.setRaw("auth", "email_login_enabled", "false")
	h.setRaw("auth", "email_registration_enabled", "true")
	h.setRaw("auth", "turnstile_registration_enabled", "true")
	h.setRaw("chat", "model_option_policy_mode", "strange")
	h.setRaw("chat", "model_option_allowed_paths", "")
	h.setRaw("chat", "context_window_fallback_tokens", "10")
	h.setRaw("desktop", "download_url", "javascript:alert(1)")
	if err := h.svc.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	rt := h.svc.Runtime()
	if rt.Chat.ContextCompactTriggerPercent != 80 || rt.MCP.ToolTimeoutSeconds != 10 || rt.MCP.MaxSelectedToolsPerMessage != 128 {
		t.Errorf("clamps: %+v %+v", rt.Chat.ContextCompactTriggerPercent, rt.MCP)
	}
	if rt.Auth.TokenTTLHours != 24 || rt.Auth.EmailRegistrationEnabled || rt.Auth.TurnstileRegistrationEnabled {
		t.Errorf("auth normalisation: %+v", rt.Auth)
	}
	if rt.Chat.ModelOptionPolicyMode != "allowlist" || rt.Chat.ModelOptionAllowedPaths == "" || rt.Chat.ContextWindowFallbackTokens != 128000 {
		t.Errorf("chat normalisation: %+v", rt.Chat)
	}
	if rt.Desktop.DownloadURL != settings.RegistryDefault("desktop", "download_url") {
		t.Errorf("desktop url: %q", rt.Desktop.DownloadURL)
	}
	// the stored values are untouched by normalisation
	if h.stored("chat", "context_compact_trigger_percent") != "5" {
		t.Fatal("normalisation must not rewrite storage")
	}
}

func TestFullContextLimitsWhenDisabled(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.mustPatch(set("file", "file_full_context_max_bytes", "1000"), set("file", "full_context_max_tokens", "500"), set("file", "full_context_pdf_max_pages", "7"))
	f := h.svc.Runtime().File
	if f.FullContextMaxBytes != 1000 || f.FullContextMaxTokens != 500 || f.FullContextPDFMaxPages != 7 {
		t.Fatalf("limits: %+v", f)
	}
	h.mustPatch(set("file", "full_context_limit_enabled", "false"))
	f = h.svc.Runtime().File
	if f.FullContextMaxBytes != 0 || f.FullContextMaxTokens != 0 || f.FullContextPDFMaxPages != 0 {
		t.Fatalf("limits must be zero: %+v", f)
	}
	if h.val("file", "file_full_context_max_bytes") != "1000" {
		t.Fatal("stored limits stay")
	}
	h.mustPatch(set("file", "full_context_limit_enabled", "true"))
	if h.svc.Runtime().File.FullContextMaxBytes != 1000 {
		t.Fatal("limits return when re-enabled")
	}
	// empty optional limits mean unlimited
	h.mustPatch(set("file", "file_full_context_max_bytes", ""), set("file", "image_max_bytes", ""))
	if h.svc.Runtime().File.FullContextMaxBytes != 0 || h.svc.Runtime().File.ImageMaxBytes != 0 {
		t.Fatal("empty means 0/unlimited")
	}
}

func TestAuthSettingsReachTheConfigHolder(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.mustPatch(set("auth", "token_ttl_hours", "48"), set("auth", "refresh_token_ttl_hours", "100"),
		set("auth", "rate_limit_enabled", "true"), set("auth", "rate_limit_rpm", "90"), set("auth", "public_auth_rate_limit_rpm", "45"))
	a := h.env.Deps.Config.Load().Dynamic.Auth
	if a.AccessTokenTTL != 48*time.Hour || a.RefreshTokenTTL != 100*time.Hour || !a.RateLimitEnabled || a.RateLimitAuthRPM != 90 || a.RateLimitPublicRPM != 45 {
		t.Fatalf("dynamic auth config: %+v", a)
	}
	// static sections are left alone
	if h.env.Deps.Config.Load().Server.HTTPPort != "8080" {
		t.Fatal("static config changed")
	}
}

func TestStartupProjectsStoredAuthValues(t *testing.T) {
	// Values stored before the area mounts are visible after a restart.
	h := newHarness(t, settings.Ports{})
	h.mustPatch(set("auth", "token_ttl_hours", "72"))
	if err := h.svc.Seed(ctxBG()); err != nil {
		t.Fatal(err)
	}
	if err := h.svc.Reload(ctxBG()); err != nil {
		t.Fatal(err)
	}
	if h.env.Deps.Config.Load().Dynamic.Auth.AccessTokenTTL != 72*time.Hour {
		t.Fatal("stored ttl not applied")
	}
}

func TestDecryptionFailureSurfaces(t *testing.T) {
	h := newHarness(t, settings.Ports{})
	h.setRaw("auth", "smtp_password", "v1:not-valid-ciphertext")
	if err := h.svc.Reload(ctxBG()); err == nil {
		t.Fatal("a sensitive value that cannot be decrypted must fail the load")
	}
	if _, err := h.svc.NamespaceValues(ctxBG(), "auth"); err == nil {
		t.Fatal("runtime read must fail")
	}
	// admin listing only reports configured-ness and keeps working
	w := h.do("GET", "/api/v1/admin/settings", h.admin, nil)
	testkit.Expect(t, w, 200, "")
	// the model option policy endpoint reads chat only and is unaffected
	testkit.Expect(t, h.do("GET", "/api/v1/settings/model-option-policy", h.user, nil), 200, "")
	// a failing runtime read is a 500 for readers of the failing namespace
	_ = config.Default
}
