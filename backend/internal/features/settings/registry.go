package settings

import (
	"sort"
	"strings"
)

type valueType string

const (
	typeString valueType = "string"
	typeInt    valueType = "int"
	typeBool   valueType = "bool"
	typeJSON   valueType = "json"
)

// definition describes one legal (namespace, key) pair.
type definition struct {
	ns, key   string
	typ       valueType
	def       string
	desc      string
	sensitive bool
	check     check
	group     codeGroup
	// target selects the Runtime field the value is projected into; nil for
	// module-read entries and entries read directly.
	target    func(*Runtime) any
	emptyZero bool
}

type option func(*definition)

func secret() option                   { return func(d *definition) { d.sensitive = true } }
func rules(cs ...check) option         { return func(d *definition) { d.check = allOf(cs...) } }
func into(f func(*Runtime) any) option { return func(d *definition) { d.target = f } }
func emptyIsZero() option              { return func(d *definition) { d.emptyZero = true } }

type registryBuilder struct{ list []*definition }

func (b *registryBuilder) add(ns, key string, t valueType, def, desc string, opts ...option) {
	d := &definition{ns: ns, key: key, typ: t, def: def, desc: desc, group: groupOf(ns, key)}
	for _, o := range opts {
		o(d)
	}
	b.list = append(b.list, d)
}

func (b *registryBuilder) str(ns, key, def, desc string, opts ...option) {
	b.add(ns, key, typeString, def, desc, opts...)
}
func (b *registryBuilder) num(ns, key, def, desc string, opts ...option) {
	b.add(ns, key, typeInt, def, desc, opts...)
}
func (b *registryBuilder) flag(ns, key, def, desc string, opts ...option) {
	b.add(ns, key, typeBool, def, desc, append([]option{rules(isBool())}, opts...)...)
}
func (b *registryBuilder) doc(ns, key, def, desc string, opts ...option) {
	b.add(ns, key, typeJSON, def, desc, opts...)
}

var embeddingGroupKeys = map[string]bool{
	"embedding_enabled": true, "embedding_host": true, "embedding_protocol": true, "embedding_key": true,
	"embedding_timeout_seconds": true, "embedding_output_dimensions": true, "embedding_normalize": true,
	"embedding_model_signature": true, "embed_trigger_on_upload": true, "embed_chunk_size_tokens": true,
	"embed_chunk_overlap_tokens": true, "embed_batch_size": true,
}

// groupOf picks the public error code family for failures of a key.
func groupOf(ns, key string) codeGroup {
	switch {
	case ns == "auth" && strings.HasPrefix(key, "smtp_"):
		return groupSMTP
	case ns == "billing" && (key == "payment_providers" || strings.HasPrefix(key, "epay_") || strings.HasPrefix(key, "stripe_")):
		return groupBilling
	case ns == "chat" && strings.HasPrefix(key, "model_option_"):
		return groupModelOption
	case ns == "file" && embeddingGroupKeys[key]:
		return groupEmbedding
	case ns == "extract":
		return groupExtract
	}
	return groupValue
}

// defaultMimeTypes is the current default allow list of upload MIME types.
const defaultMimeTypes = "image/jpeg,image/png,image/webp,image/gif,video/mp4,video/webm,video/quicktime,video/mpeg," +
	"audio/mpeg,audio/wav,audio/mp4,audio/aac,audio/ogg,audio/flac,audio/aiff," +
	"text/plain,text/markdown,text/csv,text/yaml,application/json,application/yaml,application/x-yaml,application/toml," +
	"application/pdf,application/msword," +
	"application/vnd.openxmlformats-officedocument.wordprocessingml.document," +
	"application/vnd.openxmlformats-officedocument.spreadsheetml.sheet,application/vnd.ms-excel"

const defaultEpayTypes = `[{"name":"Alipay","type":"alipay"},{"name":"WeChat Pay","type":"wxpay"}]`

// namespaces lists every legal namespace in a stable order.
var namespaces = []string{"auth", "billing", "chat", "knowledgebase", "personal_provider", "desktop", "storage", "file", "extract", "mcp", "circuit"}

// obsoleteKeys are removed from storage on every start.
var obsoleteKeys = [][2]string{
	{"mcp", "mcp_connect_timeout_ms"},
	{"mcp", "mcp_tool_timeout_ms"},
	{"chat", "context_max_input_tokens"},
	{"chat", "context_compact_trigger_tokens"},
	{"chat", "process_trace_visible_to_user"},
	{"chat", "process_trace_store_upstream_think"},
	{"chat", "models_dev_enabled"},
	{"chat", "models_dev_url"},
}

type registry struct {
	list []*definition
	idx  map[string]*definition
	ns   map[string]bool
}

func (r *registry) lookup(ns, key string) *definition { return r.idx[ns+"\x00"+key] }

// byNamespace returns the definitions of one namespace in registry order.
func (r *registry) byNamespace(ns string) []*definition {
	var out []*definition
	for _, d := range r.list {
		if d.ns == ns {
			out = append(out, d)
		}
	}
	return out
}

var reg = buildRegistry()

func buildRegistry() *registry {
	b := &registryBuilder{}
	addAuth(b)
	addBilling(b)
	addChat(b)
	addSmall(b)
	addFile(b)
	addExtract(b)
	addMCP(b)
	addCircuit(b)

	r := &registry{list: b.list, idx: map[string]*definition{}, ns: map[string]bool{}}
	for _, d := range b.list {
		k := d.ns + "\x00" + d.key
		if r.idx[k] != nil {
			panic("settings: duplicate definition " + d.ns + "/" + d.key)
		}
		r.idx[k] = d
		r.ns[d.ns] = true
	}
	for _, n := range namespaces {
		if !r.ns[n] {
			panic("settings: namespace without definitions: " + n)
		}
	}
	return r
}

func addAuth(b *registryBuilder) {
	b.num("auth", "token_ttl_hours", "24", "Access token lifetime in hours", rules(intRange(1, 168)), into(func(r *Runtime) any { return &r.Auth.TokenTTLHours }))
	b.num("auth", "refresh_token_ttl_hours", "720", "Refresh token lifetime in hours", rules(intRange(1, 8760)), into(func(r *Runtime) any { return &r.Auth.RefreshTokenTTLHours }))
	b.num("auth", "login_max_failures", "5", "Failed logins before an account is locked (0 disables lockout)", rules(isInt()), into(func(r *Runtime) any { return &r.Auth.LoginMaxFailures }))
	b.num("auth", "login_lock_minutes", "15", "Minutes an account stays locked (0 disables lockout)", rules(isInt()), into(func(r *Runtime) any { return &r.Auth.LoginLockMinutes }))
	b.flag("auth", "rate_limit_enabled", "false", "Enable request rate limiting", into(func(r *Runtime) any { return &r.Auth.RateLimitEnabled }))
	b.num("auth", "rate_limit_rpm", "60", "Authenticated requests per minute per user", rules(intRange(1, 100000)), into(func(r *Runtime) any { return &r.Auth.RateLimitRPM }))
	b.num("auth", "public_auth_rate_limit_rpm", "30", "Public requests per minute per client address", rules(intRange(1, 100000)), into(func(r *Runtime) any { return &r.Auth.PublicAuthRateLimitRPM }))
	b.str("auth", "login_default_next_path", "/chat", "Path opened after sign-in when none was requested", rules(loginPathCheck()))
	b.flag("auth", "username_login_enabled", "true", "Allow signing in with a username", into(func(r *Runtime) any { return &r.Auth.UsernameLoginEnabled }))
	b.flag("auth", "email_login_enabled", "true", "Allow signing in with an email address", into(func(r *Runtime) any { return &r.Auth.EmailLoginEnabled }))
	b.flag("auth", "third_party_login_enabled", "true", "Allow signing in with a third-party identity", into(func(r *Runtime) any { return &r.Auth.ThirdPartyLoginEnabled }))
	b.flag("auth", "email_registration_enabled", "true", "Allow registering with an email address", into(func(r *Runtime) any { return &r.Auth.EmailRegistrationEnabled }))
	b.flag("auth", "email_verification_enabled", "false", "Require email verification", into(func(r *Runtime) any { return &r.Auth.EmailVerificationEnabled }))
	b.flag("auth", "password_reset_enabled", "false", "Allow password reset by email", into(func(r *Runtime) any { return &r.Auth.PasswordResetEnabled }))
	b.str("auth", "smtp_host", "", "SMTP server host", rules(maxChars(255)), into(func(r *Runtime) any { return &r.Auth.SMTPHost }))
	b.num("auth", "smtp_port", "587", "SMTP server port", rules(intRange(1, 65535)), into(func(r *Runtime) any { return &r.Auth.SMTPPort }))
	b.str("auth", "smtp_username", "", "SMTP account name", rules(maxChars(255)), into(func(r *Runtime) any { return &r.Auth.SMTPUsername }))
	b.str("auth", "smtp_password", "", "SMTP account password", secret(), rules(maxChars(255)), into(func(r *Runtime) any { return &r.Auth.SMTPPassword }))
	b.str("auth", "smtp_from", "", "Sender address of outgoing mail", rules(maxChars(255)), into(func(r *Runtime) any { return &r.Auth.SMTPFrom }))
	b.str("auth", "email_registration_allowed_domains", "", "Email domains allowed to register (empty means any)", rules(maxChars(1024), domainListCheck()), into(func(r *Runtime) any { return &r.Auth.EmailRegistrationAllowedDomains }))
	b.flag("auth", "email_registration_block_plus_alias", "false", "Reject plus-addressed email aliases at registration", into(func(r *Runtime) any { return &r.Auth.EmailRegistrationBlockPlusAlias }))
	b.flag("auth", "auto_link_verified_email", "true", "Link third-party sign-ins to accounts with the same verified email", into(func(r *Runtime) any { return &r.Auth.AutoLinkVerifiedEmail }))
	b.flag("auth", "turnstile_registration_enabled", "false", "Require a Turnstile challenge at registration", into(func(r *Runtime) any { return &r.Auth.TurnstileRegistrationEnabled }))
	b.str("auth", "turnstile_site_key", "", "Turnstile site key", rules(maxChars(512)), into(func(r *Runtime) any { return &r.Auth.TurnstileSiteKey }))
	b.str("auth", "turnstile_secret_key", "", "Turnstile secret key", secret(), rules(maxChars(512)), into(func(r *Runtime) any { return &r.Auth.TurnstileSecretKey }))
}

func addBilling(b *registryBuilder) {
	b.str("billing", "mode", "self", "Billing mode: self, period or usage", rules(oneOf("self", "period", "usage")))
	b.str("billing", "prepaid_amount_usd", "0", "Prepaid amount credited to new accounts, in USD", rules(floatRange(0, 1000000)))
	b.flag("billing", "native_tool_billing_enabled", "true", "Charge for vendor-hosted tool usage")
	b.doc("billing", "native_tool_pricing_json", defaultNativeToolPricingJSON, "Native tool price overrides", rules(nativeToolPricingCheck()))
	b.str("billing", "usd_to_cny_rate", "7.2", "USD to CNY exchange rate", rules(floatRange(0.000001, 1000)))
	b.str("billing", "display_currency", "USD", "Currency shown to users", rules(oneOf("USD", "CNY")))
	b.str("billing", "payment_providers", "disabled", "Enabled payment providers, comma separated", rules(providerListCheck()))
	b.str("billing", "stripe_publishable_key", "", "Stripe publishable key", rules(maxChars(512)))
	b.str("billing", "stripe_secret_key", "", "Stripe secret key", secret(), rules(maxChars(512)))
	b.str("billing", "stripe_webhook_secret", "", "Stripe webhook signing secret", secret(), rules(maxChars(512)))
	b.str("billing", "epay_gateway_url", "", "EPay gateway address", rules(maxChars(512), epayGatewayCheck()))
	b.str("billing", "epay_types", defaultEpayTypes, "EPay payment methods as a JSON array", rules(maxChars(4000), epayTypesCheck()))
	b.str("billing", "epay_pid", "", "EPay merchant id", rules(maxChars(512)))
	b.str("billing", "epay_key", "", "EPay merchant key", secret(), rules(maxChars(512)))
}

func addChat(b *registryBuilder) {
	const ns = "chat"
	b.num(ns, "max_context_messages", "20", "Messages kept in the model context", rules(isInt()), into(func(r *Runtime) any { return &r.Chat.MaxContextMessages }))
	b.num(ns, "context_max_turns", "48", "Turns kept in the model context", rules(isInt()), into(func(r *Runtime) any { return &r.Chat.ContextMaxTurns }))
	b.flag(ns, "ui_components_enabled", "true", "Allow rich UI components in replies", into(func(r *Runtime) any { return &r.Chat.UIComponentsEnabled }))
	b.flag(ns, "context_compact_enabled", "false", "Compact long conversations automatically", into(func(r *Runtime) any { return &r.Chat.ContextCompactEnabled }))
	b.num(ns, "context_window_fallback_tokens", "128000", "Context window assumed when a model does not report one", rules(intRange(16384, 16000000)), into(func(r *Runtime) any { return &r.Chat.ContextWindowFallbackTokens }))
	b.num(ns, "context_compact_trigger_percent", "80", "Context usage percent that triggers compaction (0 turns it off)", rules(triggerPercentCheck()), into(func(r *Runtime) any { return &r.Chat.ContextCompactTriggerPercent }))
	b.num(ns, "context_compact_preserve_recent_turns", "8", "Recent turns kept verbatim when compacting", rules(isInt()), into(func(r *Runtime) any { return &r.Chat.ContextCompactPreserveRecent }))
	b.str(ns, "conversation_default_model", "", "Model used for new conversations", rules(maxChars(255)), into(func(r *Runtime) any { return &r.Chat.ConversationDefaultModel }))
	b.str(ns, "conversation_task_model", "follow", "Model used for titles and labels", into(func(r *Runtime) any { return &r.Chat.ConversationTaskModel }))
	b.str(ns, "conversation_title_prompt", "", "Prompt that generates conversation titles", into(func(r *Runtime) any { return &r.Chat.ConversationTitlePrompt }))
	b.str(ns, "conversation_labels_prompt", "", "Prompt that generates conversation labels", into(func(r *Runtime) any { return &r.Chat.ConversationLabelsPrompt }))
	b.str(ns, "default_system_prompt", "", "System prompt applied to every conversation", rules(maxChars(20000)), into(func(r *Runtime) any { return &r.Chat.DefaultSystemPrompt }))
	b.str(ns, "skills_prompt", "", "Instruction text introducing available skills", rules(maxChars(20000)), into(func(r *Runtime) any { return &r.Chat.SkillsPrompt }))
	b.str(ns, "model_option_policy_mode", "allowlist", "Model option policy: allowlist, denylist or disabled", rules(oneOf("allowlist", "denylist", "disabled")), into(func(r *Runtime) any { return &r.Chat.ModelOptionPolicyMode }))
	b.doc(ns, "model_option_allowed_paths", defaultAllowedPathsJSON, "Request parameters users may pass through", rules(modelOptionDocCheck()), into(func(r *Runtime) any { return &r.Chat.ModelOptionAllowedPaths }))
	b.doc(ns, "model_option_denied_paths", defaultDeniedPathsJSON, "Request parameters that are always blocked", rules(modelOptionDocCheck()), into(func(r *Runtime) any { return &r.Chat.ModelOptionDeniedPaths }))
	b.flag(ns, "rag_enabled", "false", "Retrieve knowledge for replies", into(func(r *Runtime) any { return &r.Chat.RAGEnabled }))
	b.str(ns, "rag_min_similarity", "0.45", "Minimum similarity of retrieved chunks", rules(floatRange(0.000001, 1)), into(func(r *Runtime) any { return &r.Chat.RAGMinSimilarity }))
	b.num(ns, "rag_token_budget", "2000", "Token budget for retrieved text", rules(intRange(128, 100000)), into(func(r *Runtime) any { return &r.Chat.RAGTokenBudget }))
	b.num(ns, "rag_fetch_multiplier", "3", "Over-fetch factor before ranking", rules(intRange(1, 20)), into(func(r *Runtime) any { return &r.Chat.RAGFetchMultiplier }))
	b.num(ns, "rag_wait_ready_ms", "3000", "Milliseconds to wait for files that are still indexing", rules(intRange(1000, 120000)), into(func(r *Runtime) any { return &r.Chat.RAGWaitReadyMS }))
	b.num(ns, "rag_query_history_turns", "0", "Earlier turns folded into the retrieval query", rules(intRange(0, 20)), into(func(r *Runtime) any { return &r.Chat.RAGQueryHistoryTurns }))
	b.num(ns, "rag_retrieval_cache_ttl_seconds", "120", "Seconds a retrieval result is cached", rules(intRange(0, 86400)), into(func(r *Runtime) any { return &r.Chat.RAGRetrievalCacheTTLSeconds }))
	b.flag(ns, "rag_hybrid_enabled", "false", "Combine keyword and vector retrieval", into(func(r *Runtime) any { return &r.Chat.RAGHybridEnabled }))
	b.num(ns, "context_compact_highlights_per_role", "6", "Highlights kept per role when compacting", rules(isInt()), into(func(r *Runtime) any { return &r.Chat.ContextCompactHighlightsPerRole }))
	b.num(ns, "context_compact_snippet_chars", "140", "Characters kept per highlight", rules(isInt()), into(func(r *Runtime) any { return &r.Chat.ContextCompactSnippetChars }))
	b.flag(ns, "compact_llm_enabled", "true", "Summarise with a model when compacting", into(func(r *Runtime) any { return &r.Chat.CompactLLMEnabled }))
	b.str(ns, "compact_task_model", "follow", "Model used for compaction", into(func(r *Runtime) any { return &r.Chat.CompactTaskModel }))
	b.flag(ns, "compact_async_enabled", "true", "Compact in the background", into(func(r *Runtime) any { return &r.Chat.CompactAsyncEnabled }))
	b.num(ns, "compact_max_failures", "3", "Consecutive compaction failures before giving up", rules(isInt()), into(func(r *Runtime) any { return &r.Chat.CompactMaxFailures }))
	b.str(ns, "compact_system_prompt", "", "System prompt for model-based compaction", into(func(r *Runtime) any { return &r.Chat.CompactSystemPrompt }))
	b.str(ns, "compact_light_prompt", "", "Prompt for light compaction", into(func(r *Runtime) any { return &r.Chat.CompactLightPrompt }))
	b.flag(ns, "context_token_budget_enabled", "true", "Trim the context to the token budget", into(func(r *Runtime) any { return &r.Chat.ContextTokenBudgetEnabled }))
	b.flag(ns, "message_embedding_enabled", "false", "Embed messages for history recall", into(func(r *Runtime) any { return &r.Chat.MessageEmbeddingEnabled }))
	b.flag(ns, "semantic_context_enabled", "false", "Recall earlier messages by meaning", into(func(r *Runtime) any { return &r.Chat.SemanticContextEnabled }))
	b.flag(ns, "process_trace_enabled", "true", "Record the reasoning and tool trace of a reply", into(func(r *Runtime) any { return &r.Chat.ProcessTraceEnabled }))
	b.flag(ns, "process_trace_persist_inflight", "true", "Save the trace while a reply is still running", into(func(r *Runtime) any { return &r.Chat.ProcessTracePersistInflight }))
	b.num(ns, "context_artifact_retention_days", "90", "Days generated context artifacts are kept", rules(intRange(0, 3650)), into(func(r *Runtime) any { return &r.Chat.ContextArtifactRetentionDays }))
}

func addSmall(b *registryBuilder) {
	b.flag("knowledgebase", "enabled", "true", "Switch the knowledge base feature on or off", into(func(r *Runtime) any { return &r.KnowledgeBase.Enabled }))

	b.flag("personal_provider", "enabled", "false", "Let users add their own model providers", into(func(r *Runtime) any { return &r.PersonalProvider.Enabled }))
	b.num("personal_provider", "max_per_user", "10", "Personal providers allowed per user", rules(intRange(1, 50)), into(func(r *Runtime) any { return &r.PersonalProvider.MaxPerUser }))
	b.str("personal_provider", "blocked_hosts", "", "Hosts users may not use for personal providers", rules(maxChars(4096), hostListCheck()), into(func(r *Runtime) any { return &r.PersonalProvider.BlockedHosts }))

	b.flag("desktop", "download_enabled", "true", "Show the desktop download entry", into(func(r *Runtime) any { return &r.Desktop.DownloadEnabled }))
	b.str("desktop", "download_url", defaultDesktopURL, "Desktop download address", rules(downloadURLCheck()), into(func(r *Runtime) any { return &r.Desktop.DownloadURL }))

	b.num("storage", "user_storage_quota_bytes", "104857600", "Storage quota per user in bytes (0 means unlimited)", rules(intMin(0)), into(func(r *Runtime) any { return &r.Storage.UserStorageQuotaBytes }))
	b.num("storage", "max_upload_file_bytes", "20971520", "Largest accepted upload in bytes", rules(intMin(1)), into(func(r *Runtime) any { return &r.Storage.MaxUploadFileBytes }))
	b.num("storage", "max_message_files", "10", "Files allowed on one message", rules(intRange(1, 50)), into(func(r *Runtime) any { return &r.Storage.MaxMessageFiles }))
}

func addFile(b *registryBuilder) {
	const ns = "file"
	b.num(ns, "image_max_dimension", "1024", "Longest image side in pixels before resizing (0 means no resize)", rules(intRange(0, 8192)), into(func(r *Runtime) any { return &r.File.ImageMaxDimension }))
	b.flag(ns, "full_context_limit_enabled", "true", "Apply limits to files placed in full in the context", into(func(r *Runtime) any { return &r.File.FullContextLimitEnabled }))
	b.num(ns, "file_full_context_max_bytes", "2097152", "Largest file placed in full in the context, in bytes", rules(optIntMin(0)), emptyIsZero(), into(func(r *Runtime) any { return &r.File.FullContextMaxBytes }))
	b.num(ns, "full_context_max_tokens", "65536", "Largest file placed in full in the context, in tokens", rules(optIntRange(128, 1000000)), emptyIsZero(), into(func(r *Runtime) any { return &r.File.FullContextMaxTokens }))
	b.num(ns, "image_max_bytes", "", "Largest image in bytes (empty follows the upload limit)", rules(optIntMin(1)), emptyIsZero(), into(func(r *Runtime) any { return &r.File.ImageMaxBytes }))
	b.num(ns, "doc_max_bytes", "", "Largest document in bytes (empty follows the upload limit)", rules(optIntMin(1)), emptyIsZero(), into(func(r *Runtime) any { return &r.File.DocMaxBytes }))
	b.num(ns, "full_context_pdf_max_pages", "20", "Most PDF pages placed in full in the context", rules(optIntRange(1, 500)), emptyIsZero(), into(func(r *Runtime) any { return &r.File.FullContextPDFMaxPages }))
	b.str(ns, "allowed_mime_types", defaultMimeTypes, "MIME types accepted for upload", rules(mimeListCheck()), into(func(r *Runtime) any { return &r.File.AllowedMimeTypes }))
	b.flag(ns, "embedding_enabled", "false", "Switch the embedding service on", into(func(r *Runtime) any { return &r.File.EmbeddingEnabled }))
	b.str(ns, "embedding_host", "", "Embedding service address", rules(trustedHTTPURL()), into(func(r *Runtime) any { return &r.File.EmbeddingHost }))
	b.str(ns, "embedding_protocol", "openai", "Embedding API protocol", rules(oneOf("openai", "gemini", "voyage", "jina")), into(func(r *Runtime) any { return &r.File.EmbeddingProtocol }))
	b.str(ns, "embedding_key", "", "Embedding service credential", secret(), into(func(r *Runtime) any { return &r.File.EmbeddingKey }))
	b.num(ns, "embedding_timeout_seconds", "60", "Embedding request timeout in seconds", rules(intRange(1, 600)), into(func(r *Runtime) any { return &r.File.EmbeddingTimeoutSeconds }))
	b.num(ns, "embedding_output_dimensions", "1536", "Vector dimensions", rules(intRange(64, 4096)), into(func(r *Runtime) any { return &r.File.EmbeddingOutputDimensions }))
	b.str(ns, "embedding_dimensions_policy", "send", "Whether requests carry the dimensions: send or omit", rules(oneOf("send", "omit")), into(func(r *Runtime) any { return &r.File.EmbeddingDimensionsPolicy }))
	b.flag(ns, "embedding_normalize", "true", "Normalise vectors", into(func(r *Runtime) any { return &r.File.EmbeddingNormalize }))
	b.str(ns, "embedding_model_signature", "", "Signature of the embedding space (maintained by the server)", into(func(r *Runtime) any { return &r.File.EmbeddingModelSignature }))
	b.flag(ns, "embed_trigger_on_upload", "true", "Embed files as soon as they are uploaded", into(func(r *Runtime) any { return &r.File.EmbedTriggerOnUpload }))
	b.num(ns, "embed_chunk_size_tokens", "1024", "Chunk size in tokens", rules(isInt()), into(func(r *Runtime) any { return &r.File.EmbedChunkSizeTokens }))
	b.num(ns, "embed_chunk_overlap_tokens", "64", "Chunk overlap in tokens", rules(isInt()), into(func(r *Runtime) any { return &r.File.EmbedChunkOverlapTokens }))
	b.num(ns, "embed_batch_size", "20", "Chunks per embedding request", rules(isInt()), into(func(r *Runtime) any { return &r.File.EmbedBatchSize }))
	b.num(ns, "rag_top_k", "5", "Chunks retrieved per query", rules(intRange(1, 50)), into(func(r *Runtime) any { return &r.File.RAGTopK }))
	b.str(ns, "rag_model", "sentence-transformers/all-MiniLM-L6-v2", "Embedding model name", into(func(r *Runtime) any { return &r.File.RAGModel }))
}

func addExtract(b *registryBuilder) {
	const ns = "extract"
	timeout := func(max int64, f func(*Runtime) any) []option {
		return []option{rules(intRange(1, max)), into(f)}
	}
	b.str(ns, "engine", "builtin", "Text extraction engine", rules(oneOf("builtin", "tika", "docling", "mineru")), into(func(r *Runtime) any { return &r.Extract.Engine }))
	b.str(ns, "ocr_engine", "rapidocr", "OCR engine", rules(oneOf("rapidocr", "tesseract", "paddle", "tencent", "aliyun", "mistral", "llm")), into(func(r *Runtime) any { return &r.Extract.OCREngine }))
	b.flag(ns, "image_ocr_enabled", "false", "Run OCR on images", into(func(r *Runtime) any { return &r.Extract.ImageOCREnabled }))
	b.flag(ns, "pdf_ocr_fallback_enabled", "false", "Run OCR on PDFs without a text layer", into(func(r *Runtime) any { return &r.Extract.PDFOCRFallbackEnabled }))

	b.str(ns, "tika_source", "external", "Tika deployment: external or managed", rules(oneOf("external", "managed")), into(func(r *Runtime) any { return &r.Extract.TikaSource }))
	b.str(ns, "tika_base_url", "http://127.0.0.1:9998", "Tika server address", rules(optHTTPURL()), into(func(r *Runtime) any { return &r.Extract.TikaBaseURL }))
	b.num(ns, "tika_timeout_seconds", "60", "Tika request timeout in seconds", timeout(120, func(r *Runtime) any { return &r.Extract.TikaTimeoutSeconds })...)
	b.str(ns, "tika_auth_token", "", "Tika access token", secret(), into(func(r *Runtime) any { return &r.Extract.TikaAuthToken }))

	b.str(ns, "docling_base_url", "http://127.0.0.1:8005/ocr", "Docling server address", rules(optHTTPURL()), into(func(r *Runtime) any { return &r.Extract.DoclingBaseURL }))
	b.str(ns, "docling_auth_token", "", "Docling access token", secret(), into(func(r *Runtime) any { return &r.Extract.DoclingAuthToken }))
	b.num(ns, "docling_timeout_seconds", "60", "Docling request timeout in seconds", timeout(600, func(r *Runtime) any { return &r.Extract.DoclingTimeoutSeconds })...)

	b.str(ns, "tesseract_ocr_base_url", "http://127.0.0.1:8004/ocr", "Tesseract OCR server address", rules(optHTTPURL()), into(func(r *Runtime) any { return &r.Extract.TesseractBaseURL }))
	b.str(ns, "tesseract_ocr_auth_token", "", "Tesseract OCR access token", secret(), into(func(r *Runtime) any { return &r.Extract.TesseractAuthToken }))
	b.num(ns, "tesseract_ocr_timeout_seconds", "60", "Tesseract OCR request timeout in seconds", timeout(600, func(r *Runtime) any { return &r.Extract.TesseractTimeoutSeconds })...)

	b.str(ns, "rapidocr_source", "external", "RapidOCR deployment: external or managed", rules(oneOf("external", "managed")), into(func(r *Runtime) any { return &r.Extract.RapidOCRSource }))
	b.str(ns, "rapidocr_base_url", "http://127.0.0.1:8002/ocr", "RapidOCR server address", rules(optHTTPURL()), into(func(r *Runtime) any { return &r.Extract.RapidOCRBaseURL }))
	b.str(ns, "rapidocr_auth_token", "", "RapidOCR access token", secret(), into(func(r *Runtime) any { return &r.Extract.RapidOCRAuthToken }))
	b.num(ns, "rapidocr_timeout_seconds", "60", "RapidOCR request timeout in seconds", timeout(600, func(r *Runtime) any { return &r.Extract.RapidOCRTimeoutSeconds })...)

	b.str(ns, "paddle_ocr_base_url", "", "PaddleOCR server address", rules(optHTTPURL()), into(func(r *Runtime) any { return &r.Extract.PaddleBaseURL }))
	b.str(ns, "paddle_ocr_auth_token", "", "PaddleOCR access token", secret(), into(func(r *Runtime) any { return &r.Extract.PaddleAuthToken }))
	b.num(ns, "paddle_ocr_timeout_seconds", "60", "PaddleOCR request timeout in seconds", timeout(600, func(r *Runtime) any { return &r.Extract.PaddleTimeoutSeconds })...)

	b.str(ns, "tencent_ocr_secret_id", "", "Tencent Cloud secret id", rules(maxChars(512)), into(func(r *Runtime) any { return &r.Extract.TencentSecretID }))
	b.str(ns, "tencent_ocr_secret_key", "", "Tencent Cloud secret key", secret(), rules(maxChars(512)), into(func(r *Runtime) any { return &r.Extract.TencentSecretKey }))
	b.str(ns, "tencent_ocr_region", "ap-guangzhou", "Tencent Cloud region", rules(maxChars(255)), into(func(r *Runtime) any { return &r.Extract.TencentRegion }))
	b.str(ns, "tencent_ocr_endpoint", "ocr.tencentcloudapi.com", "Tencent Cloud OCR endpoint", rules(maxChars(255)), into(func(r *Runtime) any { return &r.Extract.TencentEndpoint }))
	b.num(ns, "tencent_ocr_timeout_seconds", "60", "Tencent OCR request timeout in seconds", timeout(600, func(r *Runtime) any { return &r.Extract.TencentTimeoutSeconds })...)

	b.str(ns, "aliyun_ocr_access_key_id", "", "Alibaba Cloud access key id", rules(maxChars(512)), into(func(r *Runtime) any { return &r.Extract.AliyunAccessKeyID }))
	b.str(ns, "aliyun_ocr_access_key_secret", "", "Alibaba Cloud access key secret", secret(), rules(maxChars(512)), into(func(r *Runtime) any { return &r.Extract.AliyunAccessKeySecret }))
	b.str(ns, "aliyun_ocr_region", "cn-hangzhou", "Alibaba Cloud region", rules(maxChars(255)), into(func(r *Runtime) any { return &r.Extract.AliyunRegion }))
	b.str(ns, "aliyun_ocr_endpoint", "ocr-api.cn-hangzhou.aliyuncs.com", "Alibaba Cloud OCR endpoint", rules(maxChars(255)), into(func(r *Runtime) any { return &r.Extract.AliyunEndpoint }))
	b.num(ns, "aliyun_ocr_timeout_seconds", "60", "Alibaba OCR request timeout in seconds", timeout(600, func(r *Runtime) any { return &r.Extract.AliyunTimeoutSeconds })...)

	b.str(ns, "mineru_source", "cloud", "MinerU deployment: cloud or self_hosted", rules(oneOf("cloud", "self_hosted")), into(func(r *Runtime) any { return &r.Extract.MinerUSource }))
	b.str(ns, "mineru_base_url", "https://mineru.net/api/v4", "MinerU address", rules(optHTTPURL()), into(func(r *Runtime) any { return &r.Extract.MinerUBaseURL }))
	b.str(ns, "mineru_file_types", "pdf,word,presentation", "File kinds sent to MinerU", rules(fileTypeListCheck()), into(func(r *Runtime) any { return &r.Extract.MinerUFileTypes }))
	b.str(ns, "mineru_auth_token", "", "MinerU access token", secret(), into(func(r *Runtime) any { return &r.Extract.MinerUAuthToken }))
	b.num(ns, "mineru_timeout_seconds", "180", "MinerU request timeout in seconds", timeout(600, func(r *Runtime) any { return &r.Extract.MinerUTimeoutSeconds })...)

	b.str(ns, "mistral_ocr_base_url", "https://api.mistral.ai/v1/ocr", "Mistral OCR address", rules(trustedHTTPURL()), into(func(r *Runtime) any { return &r.Extract.MistralBaseURL }))
	b.str(ns, "mistral_ocr_auth_token", "", "Mistral OCR access token", secret(), into(func(r *Runtime) any { return &r.Extract.MistralAuthToken }))
	b.str(ns, "mistral_ocr_model", "mistral-ocr-latest", "Mistral OCR model", into(func(r *Runtime) any { return &r.Extract.MistralModel }))
	b.num(ns, "mistral_ocr_timeout_seconds", "60", "Mistral OCR request timeout in seconds", timeout(600, func(r *Runtime) any { return &r.Extract.MistralTimeoutSeconds })...)

	b.str(ns, "llm_ocr_base_url", "", "Vision model address used for OCR", rules(optHTTPURL()), into(func(r *Runtime) any { return &r.Extract.LLMOCRBaseURL }))
	b.str(ns, "llm_ocr_model", "", "Vision model used for OCR", into(func(r *Runtime) any { return &r.Extract.LLMOCRModel }))
	b.str(ns, "llm_ocr_auth_token", "", "Vision model access token", secret(), into(func(r *Runtime) any { return &r.Extract.LLMOCRAuthToken }))
	b.num(ns, "llm_ocr_timeout_seconds", "60", "Vision OCR request timeout in seconds", timeout(600, func(r *Runtime) any { return &r.Extract.LLMOCRTimeoutSeconds })...)
	b.str(ns, "llm_ocr_prompt", "", "Prompt sent to the vision model for OCR", into(func(r *Runtime) any { return &r.Extract.LLMOCRPrompt }))
}

func addMCP(b *registryBuilder) {
	const ns = "mcp"
	b.flag(ns, "mcp_enable", "false", "Switch MCP tools on", into(func(r *Runtime) any { return &r.MCP.Enable }))
	b.num(ns, "mcp_tool_timeout_seconds", "10", "Seconds a tool call may run", rules(intRange(0, 1800)), into(func(r *Runtime) any { return &r.MCP.ToolTimeoutSeconds }))
	b.num(ns, "mcp_tool_retry_count", "0", "Retries after a failed tool call", rules(intRange(0, 5)), into(func(r *Runtime) any { return &r.MCP.ToolRetryCount }))
	b.num(ns, "mcp_max_concurrent_calls", "8", "Tool calls running at once", rules(intRange(1, 64)), into(func(r *Runtime) any { return &r.MCP.MaxConcurrentCalls }))
	b.num(ns, "mcp_max_selected_tools_per_message", "32", "Tools a user may select for one message", rules(intRange(1, 128)), into(func(r *Runtime) any { return &r.MCP.MaxSelectedToolsPerMessage }))
	b.num(ns, "mcp_max_llm_calls_per_run", "5", "Model calls in one tool run", rules(intRange(2, 32)), into(func(r *Runtime) any { return &r.MCP.MaxLLMCallsPerRun }))
	b.num(ns, "mcp_max_tool_calls_per_run", "8", "Tool calls in one run", rules(intRange(1, 64)), into(func(r *Runtime) any { return &r.MCP.MaxToolCallsPerRun }))
	b.str(ns, "mcp_tool_prompt", "", "Instruction text describing tool use", rules(maxChars(20000)), into(func(r *Runtime) any { return &r.MCP.ToolPrompt }))
}

func addCircuit(b *registryBuilder) {
	b.num("circuit", "channel_failure_threshold", "3", "Failures that open a channel circuit", rules(isInt()))
	b.num("circuit", "channel_failure_window_seconds", "120", "Window in which failures are counted, in seconds", rules(isInt()))
	b.num("circuit", "channel_circuit_open_seconds", "60", "Seconds a channel circuit stays open", rules(isInt()))
}

// ---- lookups used by other modules ---------------------------------------------

// ValidNamespace reports whether ns is a settings namespace (input trimmed).
func ValidNamespace(ns string) bool { return reg.ns[strings.TrimSpace(ns)] }

// IsSensitive reports whether (ns, key) is a registered sensitive setting
// (inputs trimmed).
func IsSensitive(ns, key string) bool {
	d := reg.lookup(strings.TrimSpace(ns), strings.TrimSpace(key))
	return d != nil && d.sensitive
}

// SettingRef names one setting.
type SettingRef struct {
	Namespace string
	Key       string
}

// SensitiveKeys lists every sensitive setting so key rotation tooling can
// re-encrypt them. The order is stable.
func SensitiveKeys() []SettingRef {
	var out []SettingRef
	for _, d := range reg.list {
		if d.sensitive {
			out = append(out, SettingRef{d.ns, d.key})
		}
	}
	return out
}

// Namespaces lists the legal namespaces, sorted.
func Namespaces() []string {
	out := append([]string(nil), namespaces...)
	sort.Strings(out)
	return out
}

// DefaultValue returns the registry default of a setting.
func DefaultValue(ns, key string) (string, bool) {
	d := reg.lookup(strings.TrimSpace(ns), strings.TrimSpace(key))
	if d == nil {
		return "", false
	}
	return d.def, true
}
