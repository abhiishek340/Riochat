import type { MessageKey } from "@/lib/i18n/provider";
import type { Draft, FieldDef, FieldOption, FieldType } from "../lib/settings-form";
import { embeddingReady, EXTRACT_ENGINES, F, MINERU_TYPES, OCR_ENGINES, ocrActive, ocrEngineOf, type OcrEngine } from "./rules";

const label = (name: string) => `adminFiles.fields.${name}.label` as MessageKey;
const hint = (name: string) => `adminFiles.fields.${name}.hint` as MessageKey;

function field(ns: string, key: string, type: FieldType, name: string, extra: Partial<FieldDef> = {}): FieldDef {
  return { ns, key, type, label: label(name), hint: hint(name), ...extra };
}

const is = (id: string, value: string) => (d: Draft) => (d[id] ?? "") === value;
const flag = (id: string, fallback = false) => (d: Draft) => (d[id] === undefined || d[id] === "" ? fallback : d[id] === "true");
const opts = (prefix: string, values: readonly string[]): FieldOption[] => values.map((value) => ({ value, labelKey: `adminFiles.options.${prefix}.${value}` as MessageKey }));

export const MINERU_CLOUD_URL = "https://mineru.net/api/v4";
export const MINERU_SELF_HOSTED_URL = "http://127.0.0.1:8000";

export const UPLOAD_FIELDS: FieldDef[] = [
  field("storage", "max_message_files", "int", "maxMessageFiles", { def: "10" }),
  field("file", "allowed_mime_types", "textarea", "allowedMimeTypes"),
  field("storage", "max_upload_file_bytes", "int", "maxUploadBytes", { unit: "mb", suffix: "MB", def: "20971520" }),
  field("storage", "user_storage_quota_bytes", "int", "userQuota", { unit: "mb", suffix: "MB", def: "104857600" }),
  field("file", "image_max_bytes", "int", "imageMax", { unit: "mb", suffix: "MB" }),
  field("file", "doc_max_bytes", "int", "docMax", { unit: "mb", suffix: "MB" }),
];

const engineIs = (engine: string) => is(F.engine, engine);
const ocrIs = (engine: OcrEngine) => (d: Draft) => ocrActive(d) && ocrEngineOf(d) === engine;
const X = "extract";
const timeout = (key: string, name: string, def: string, visible: FieldDef["visible"]) => field(X, key, "int", name, { def, blankDefault: true, suffix: "s", visible });
const ocrServer = (prefix: string, name: string, engine: OcrEngine, urlDef = ""): FieldDef[] => [
  field(X, `${prefix}_base_url`, "string", `${name}Url`, { def: urlDef, visible: ocrIs(engine) }),
  field(X, `${prefix}_auth_token`, "password", `${name}Token`, { secret: true, visible: ocrIs(engine) }),
  timeout(`${prefix}_timeout_seconds`, `${name}Timeout`, "60", ocrIs(engine)),
];

export const EXTRACTION_FIELDS: FieldDef[] = [
  field(X, "engine", "select", "engine", { def: "builtin", options: opts("engines", EXTRACT_ENGINES) }),
  field(X, "tika_base_url", "string", "tikaUrl", { def: "http://127.0.0.1:9998", visible: engineIs("tika") }),
  field(X, "tika_auth_token", "password", "tikaToken", { secret: true, visible: engineIs("tika") }),
  timeout("tika_timeout_seconds", "tikaTimeout", "60", engineIs("tika")),
  field(X, "docling_base_url", "string", "doclingUrl", { def: "http://127.0.0.1:8005/ocr", visible: engineIs("docling") }),
  field(X, "docling_auth_token", "password", "doclingToken", { secret: true, visible: engineIs("docling") }),
  timeout("docling_timeout_seconds", "doclingTimeout", "60", engineIs("docling")),
  field(X, "mineru_source", "tabs", "mineruSource", { def: "cloud", blankDefault: true, options: opts("mineruSources", ["cloud", "self_hosted"]), visible: engineIs("mineru") }),
  field(X, "mineru_base_url", "string", "mineruUrl", { def: MINERU_CLOUD_URL, blankDefault: true, visible: engineIs("mineru") }),
  field(X, "mineru_file_types", "multi", "mineruTypes", { def: "pdf,word,presentation", blankDefault: true, options: opts("mineruTypes", MINERU_TYPES), visible: engineIs("mineru") }),
  field(X, "mineru_auth_token", "password", "mineruToken", { secret: true, visible: engineIs("mineru") }),
  timeout("mineru_timeout_seconds", "mineruTimeout", "180", engineIs("mineru")),
  field(X, "image_ocr_enabled", "bool", "imageOcr", { def: "false" }),
  field(X, "pdf_ocr_fallback_enabled", "bool", "pdfOcr", { def: "false" }),
  field(X, "ocr_engine", "select", "ocrEngine", { def: "rapidocr", blankDefault: true, options: opts("ocrEngines", OCR_ENGINES), visible: ocrActive }),
  ...ocrServer("rapidocr", "rapidocr", "rapidocr", "http://127.0.0.1:8002/ocr"),
  ...ocrServer("tesseract_ocr", "tesseract", "tesseract", "http://127.0.0.1:8004/ocr"),
  ...ocrServer("paddle_ocr", "paddle", "paddle"),
  field(X, "tencent_ocr_secret_id", "string", "tencentId", { visible: ocrIs("tencent") }),
  field(X, "tencent_ocr_secret_key", "password", "tencentKey", { secret: true, visible: ocrIs("tencent") }),
  field(X, "tencent_ocr_region", "string", "tencentRegion", { def: "ap-guangzhou", blankDefault: true, visible: ocrIs("tencent") }),
  field(X, "tencent_ocr_endpoint", "string", "tencentEndpoint", { def: "ocr.tencentcloudapi.com", blankDefault: true, visible: ocrIs("tencent") }),
  timeout("tencent_ocr_timeout_seconds", "tencentTimeout", "60", ocrIs("tencent")),
  field(X, "aliyun_ocr_access_key_id", "string", "aliyunId", { visible: ocrIs("aliyun") }),
  field(X, "aliyun_ocr_access_key_secret", "password", "aliyunSecret", { secret: true, visible: ocrIs("aliyun") }),
  field(X, "aliyun_ocr_region", "string", "aliyunRegion", { def: "cn-hangzhou", blankDefault: true, visible: ocrIs("aliyun") }),
  field(X, "aliyun_ocr_endpoint", "string", "aliyunEndpoint", { def: "ocr-api.cn-hangzhou.aliyuncs.com", blankDefault: true, visible: ocrIs("aliyun") }),
  timeout("aliyun_ocr_timeout_seconds", "aliyunTimeout", "60", ocrIs("aliyun")),
  field(X, "mistral_ocr_base_url", "string", "mistralUrl", { def: "https://api.mistral.ai/v1/ocr", blankDefault: true, visible: ocrIs("mistral") }),
  field(X, "mistral_ocr_auth_token", "password", "mistralToken", { secret: true, visible: ocrIs("mistral") }),
  field(X, "mistral_ocr_model", "string", "mistralModel", { def: "mistral-ocr-latest", blankDefault: true, visible: ocrIs("mistral") }),
  timeout("mistral_ocr_timeout_seconds", "mistralTimeout", "60", ocrIs("mistral")),
  field(X, "llm_ocr_base_url", "string", "llmUrl", { visible: ocrIs("llm") }),
  field(X, "llm_ocr_auth_token", "password", "llmToken", { secret: true, visible: ocrIs("llm") }),
  field(X, "llm_ocr_model", "string", "llmModel", { visible: ocrIs("llm") }),
  timeout("llm_ocr_timeout_seconds", "llmTimeout", "60", ocrIs("llm")),
  field(X, "llm_ocr_prompt", "textarea", "llmPrompt", { visible: ocrIs("llm") }),
];

/** Written as "external" on every extraction save; they have no controls and sit outside the rendered group. */
export const SOURCE_FIELDS: FieldDef[] = [
  { ns: X, key: "tika_source", type: "string", label: label("tikaUrl"), def: "external", blankDefault: true },
  { ns: X, key: "rapidocr_source", type: "string", label: label("rapidocrUrl"), def: "external", blankDefault: true },
];

const full = flag(F.fullLimit, true);
export const FULLTEXT_FIELDS: FieldDef[] = [
  field("file", "full_context_limit_enabled", "bool", "fullLimit", { def: "true" }),
  field("file", "file_full_context_max_bytes", "int", "fullBytes", { unit: "mb", suffix: "MB", visible: full }),
  field("file", "full_context_max_tokens", "int", "fullTokens", { visible: full }),
  field("file", "full_context_pdf_max_pages", "int", "fullPages", { visible: full }),
];

const embedOn = flag(F.embeddingOn);
export const VECTOR_FIELDS: FieldDef[] = [
  field("file", "embedding_enabled", "bool", "embeddingOn", { def: "false" }),
  field("file", "embedding_host", "string", "embeddingHost", { visible: embedOn }),
  field("file", "embedding_protocol", "select", "embeddingProtocol", { def: "openai", blankDefault: true, options: opts("embeddingProtocols", ["openai", "gemini", "voyage", "jina"]), visible: embedOn }),
  field("file", "embedding_key", "password", "embeddingKey", { secret: true, visible: embedOn }),
  field("file", "rag_model", "string", "embeddingModel", { visible: embedOn }),
  field("file", "embedding_timeout_seconds", "int", "embeddingTimeout", { def: "60", blankDefault: true, suffix: "s", visible: embedOn }),
  field("file", "embedding_output_dimensions", "int", "embeddingDims", { def: "1536", blankDefault: true, visible: embedOn }),
  field("file", "embedding_dimensions_policy", "select", "embeddingDimsPolicy", { def: "send", blankDefault: true, options: opts("dimensionPolicies", ["send", "omit"]), visible: embedOn }),
  field("file", "embedding_normalize", "bool", "embeddingNormalize", { def: "true", visible: embedOn }),
  field("file", "embed_trigger_on_upload", "bool", "embedOnUpload", { def: "true", visible: embedOn }),
  field("file", "embed_batch_size", "int", "embedBatch", { def: "20", blankDefault: true, visible: embedOn }),
  field("file", "embed_chunk_size_tokens", "int", "embedChunk", { def: "1024", blankDefault: true, visible: embedOn }),
  field("file", "embed_chunk_overlap_tokens", "int", "embedOverlap", { def: "64", blankDefault: true, visible: embedOn }),
];

export const SEMANTIC_FIELDS: FieldDef[] = [
  field("chat", "message_embedding_enabled", "bool", "messageEmbedding", { def: "false" }),
  field("chat", "semantic_context_enabled", "bool", "semanticContext", { def: "false" }),
];

const ragReady = (d: Draft) => d[F.rag] === "true" && embeddingReady(d);
export const RAG_FIELDS: FieldDef[] = [
  field("chat", "rag_enabled", "bool", "ragOn", { def: "false" }),
  field("file", "rag_top_k", "int", "ragTopK", { def: "5", blankDefault: true, visible: ragReady }),
  field("chat", "rag_min_similarity", "string", "ragMinSimilarity", { def: "0.45", blankDefault: true, visible: ragReady }),
  field("chat", "rag_token_budget", "int", "ragTokenBudget", { def: "2000", blankDefault: true, visible: ragReady }),
  field("chat", "rag_fetch_multiplier", "int", "ragFetchMultiplier", { def: "3", blankDefault: true, visible: ragReady }),
  field("chat", "rag_wait_ready_ms", "int", "ragWaitMs", { def: "3000", blankDefault: true, visible: ragReady }),
  field("chat", "rag_query_history_turns", "int", "ragHistory", { def: "0", blankDefault: true, visible: ragReady }),
  field("chat", "rag_retrieval_cache_ttl_seconds", "int", "ragCacheTtl", { def: "120", blankDefault: true, visible: ragReady }),
  field("chat", "rag_hybrid_enabled", "bool", "ragHybrid", { def: "false", visible: ragReady }),
];

export const ALL_FILE_FIELDS: FieldDef[] = [...UPLOAD_FIELDS, ...EXTRACTION_FIELDS, ...SOURCE_FIELDS, ...FULLTEXT_FIELDS, ...VECTOR_FIELDS, ...SEMANTIC_FIELDS, ...RAG_FIELDS];
