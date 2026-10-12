/** Pure rules of the files and retrieval screen. */
import { problem, type Problem } from "../lib/problem";
import { dedupeCaseInsensitive, hasValue, splitList, type Draft } from "../lib/settings-form";

export const EXTRACT_ENGINES = ["builtin", "tika", "docling", "mineru"] as const;
export const OCR_ENGINES = ["rapidocr", "tesseract", "paddle", "tencent", "aliyun", "mistral", "llm"] as const;
export type ExtractEngine = (typeof EXTRACT_ENGINES)[number];
export type OcrEngine = (typeof OCR_ENGINES)[number];

const id = (ns: string, key: string) => `${ns}.${key}`;

export const F = {
  engine: id("extract", "engine"),
  ocrEngine: id("extract", "ocr_engine"),
  imageOcr: id("extract", "image_ocr_enabled"),
  pdfOcr: id("extract", "pdf_ocr_fallback_enabled"),
  tikaUrl: id("extract", "tika_base_url"),
  doclingUrl: id("extract", "docling_base_url"),
  mineruUrl: id("extract", "mineru_base_url"),
  mineruSource: id("extract", "mineru_source"),
  mineruTypes: id("extract", "mineru_file_types"),
  mime: id("file", "allowed_mime_types"),
  embeddingOn: id("file", "embedding_enabled"),
  embeddingHost: id("file", "embedding_host"),
  embeddingModel: id("file", "rag_model"),
  embeddingDims: id("file", "embedding_output_dimensions"),
  fullLimit: id("file", "full_context_limit_enabled"),
  messageEmbedding: id("chat", "message_embedding_enabled"),
  semantic: id("chat", "semantic_context_enabled"),
  rag: id("chat", "rag_enabled"),
} as const;

const on = (draft: Draft, key: string, fallback = false) => (draft[key] === undefined || draft[key] === "" ? fallback : draft[key] === "true");
const filled = (draft: Draft, key: string) => (draft[key] ?? "").trim() !== "";

/** Enabled with a host. */
export function embeddingConfigured(draft: Draft): boolean {
  return on(draft, F.embeddingOn) && filled(draft, F.embeddingHost);
}

/** Enabled with a host and a model. */
export function embeddingReady(draft: Draft): boolean {
  return embeddingConfigured(draft) && filled(draft, F.embeddingModel);
}

/**
 * Keeps the retrieval switches consistent: without a ready embedding service
 * RAG, message embedding and semantic context are off; semantic context also
 * needs message embedding.
 */
export function normalizeRetrieval(next: Draft): Draft {
  let out = next;
  const off = (key: string) => {
    if (out[key] === "true") out = { ...out, [key]: "false" };
  };
  if (!embeddingReady(out)) {
    off(F.rag);
    off(F.messageEmbedding);
    off(F.semantic);
  }
  if (!on(out, F.messageEmbedding)) off(F.semantic);
  return out;
}

/** Turning one of the three on is refused while the embedding service is not ready. */
export function retrievalChangeProblem(draft: Draft, changedId: string, value: string): Problem | null {
  if (value !== "true") return null;
  if (changedId !== F.rag && changedId !== F.messageEmbedding && changedId !== F.semantic) return null;
  return embeddingReady(draft) ? null : problem("adminFiles.problems.needsEmbedding");
}

/** A rag model or dimension change makes stored vectors stale. */
export function changesVectorSpace(sentIds: readonly string[]): boolean {
  return sentIds.includes(F.embeddingModel) || sentIds.includes(F.embeddingDims);
}

// --- extraction -------------------------------------------------------------

const EXTRACT_URL: Partial<Record<ExtractEngine, string>> = { tika: F.tikaUrl, docling: F.doclingUrl, mineru: F.mineruUrl };

/** Fields that must have a value for the selected OCR engine (secrets count when already stored). */
export const OCR_REQUIRED: Record<OcrEngine, readonly string[]> = {
  rapidocr: ["rapidocr_base_url"],
  tesseract: ["tesseract_ocr_base_url"],
  paddle: ["paddle_ocr_base_url"],
  tencent: ["tencent_ocr_secret_id", "tencent_ocr_secret_key", "tencent_ocr_region"],
  aliyun: ["aliyun_ocr_access_key_id", "aliyun_ocr_access_key_secret", "aliyun_ocr_region"],
  mistral: ["mistral_ocr_base_url", "mistral_ocr_auth_token", "mistral_ocr_model"],
  llm: ["llm_ocr_base_url", "llm_ocr_model"],
};

export function ocrEngineOf(draft: Draft): OcrEngine {
  const v = draft[F.ocrEngine] ?? "";
  return (OCR_ENGINES as readonly string[]).includes(v) ? (v as OcrEngine) : "rapidocr";
}

export function ocrActive(draft: Draft): boolean {
  return on(draft, F.imageOcr) || on(draft, F.pdfOcr);
}

export function validateExtraction(draft: Draft, configured: ReadonlySet<string>): Problem | null {
  const engine = (draft[F.engine] ?? "builtin") as ExtractEngine;
  const urlId = EXTRACT_URL[engine];
  if (urlId && !filled(draft, urlId)) return problem("adminFiles.problems.engineUrl", { engine });
  if (ocrActive(draft)) {
    const ocr = ocrEngineOf(draft);
    for (const key of OCR_REQUIRED[ocr]) {
      if (!hasValue(draft, configured, id("extract", key))) return problem("adminFiles.problems.ocrField", { engine: ocr, field: `adminFiles.fieldNames.${key}` });
    }
  }
  return null;
}

export function validateEmbedding(draft: Draft): Problem | null {
  if (!on(draft, F.embeddingOn)) return null;
  if (!filled(draft, F.embeddingHost)) return problem("adminFiles.problems.embeddingHost");
  if (!filled(draft, F.embeddingModel)) return problem("adminFiles.problems.embeddingModel");
  return null;
}

/** Companion keys of the selected engines, always sent together with the dirty ones. */
export function companionKeys(draft: Draft): string[] {
  const keys: string[] = [];
  const engine = (draft[F.engine] ?? "builtin") as ExtractEngine;
  if (engine === "tika") keys.push("tika_base_url", "tika_timeout_seconds", "tika_auth_token");
  if (engine === "docling") keys.push("docling_base_url", "docling_timeout_seconds", "docling_auth_token");
  if (engine === "mineru") keys.push("mineru_source", "mineru_base_url", "mineru_file_types", "mineru_timeout_seconds", "mineru_auth_token");
  if (ocrActive(draft)) {
    const ocr = ocrEngineOf(draft);
    keys.push(...OCR_FIELDS[ocr]);
  }
  keys.push("tika_source", "rapidocr_source");
  return keys.map((k) => id("extract", k));
}

export const OCR_FIELDS: Record<OcrEngine, readonly string[]> = {
  rapidocr: ["rapidocr_base_url", "rapidocr_timeout_seconds", "rapidocr_auth_token"],
  tesseract: ["tesseract_ocr_base_url", "tesseract_ocr_timeout_seconds", "tesseract_ocr_auth_token"],
  paddle: ["paddle_ocr_base_url", "paddle_ocr_timeout_seconds", "paddle_ocr_auth_token"],
  tencent: ["tencent_ocr_secret_id", "tencent_ocr_secret_key", "tencent_ocr_region", "tencent_ocr_endpoint", "tencent_ocr_timeout_seconds"],
  aliyun: ["aliyun_ocr_access_key_id", "aliyun_ocr_access_key_secret", "aliyun_ocr_region", "aliyun_ocr_endpoint", "aliyun_ocr_timeout_seconds"],
  mistral: ["mistral_ocr_base_url", "mistral_ocr_auth_token", "mistral_ocr_model", "mistral_ocr_timeout_seconds"],
  llm: ["llm_ocr_base_url", "llm_ocr_auth_token", "llm_ocr_model", "llm_ocr_timeout_seconds", "llm_ocr_prompt"],
};

// --- MinerU allowlist hint --------------------------------------------------

export const MINERU_TYPES = ["pdf", "word", "presentation", "excel"] as const;
export type MineruType = (typeof MINERU_TYPES)[number];

const MIME = {
  pdf: "application/pdf",
  doc: "application/msword",
  docx: "application/vnd.openxmlformats-officedocument.wordprocessingml.document",
  ppt: "application/vnd.ms-powerpoint",
  pptx: "application/vnd.openxmlformats-officedocument.presentationml.presentation",
  xls: "application/vnd.ms-excel",
  xlsx: "application/vnd.openxmlformats-officedocument.spreadsheetml.sheet",
} as const;

const NEEDS: Record<"cloud" | "self_hosted", Record<MineruType, readonly string[]>> = {
  cloud: { pdf: [MIME.pdf], word: [MIME.doc, MIME.docx], presentation: [MIME.ppt, MIME.pptx], excel: [MIME.xls, MIME.xlsx] },
  self_hosted: { pdf: [MIME.pdf], word: [MIME.docx], presentation: [MIME.pptx], excel: [MIME.xlsx] },
};

export function parseMineruTypes(value: string): MineruType[] {
  const chosen = new Set(splitList(value));
  return MINERU_TYPES.filter((t) => chosen.has(t));
}

/** MIME types MinerU needs that a non-blank allowlist lacks (compared without case). */
export function missingMineruMimes(allowed: string, source: string, types: string): string[] {
  if (!allowed.trim()) return [];
  const have = new Set(splitList(allowed.replace(/;/g, ",")).map((m) => m.toLowerCase()));
  const need = NEEDS[source === "self_hosted" ? "self_hosted" : "cloud"];
  const missing: string[] = [];
  for (const type of parseMineruTypes(types)) for (const mime of need[type]) if (!have.has(mime.toLowerCase()) && !missing.includes(mime)) missing.push(mime);
  return missing;
}

/** The allowlist with the missing types appended: no duplicates (case-insensitive), order kept. */
export function mergeMimes(allowed: string, extra: readonly string[]): string {
  return dedupeCaseInsensitive([...splitList(allowed), ...extra]).join(",");
}

// --- runtime / embedding status ------------------------------------------------

export const TASK_BUCKETS = ["ready", "pending", "failed", "stale", "empty", "unsupported"] as const;
export type TaskBucket = (typeof TASK_BUCKETS)[number];

export interface EmbeddingStatus {
  readyCount: number;
  pendingCount: number;
  failedCount: number;
  staleCount: number;
  emptyCount: number;
  unsupportedCount: number;
  activeCount: number;
  stalledCount: number;
  needsReindex: boolean;
  reindexRunning: boolean;
  modelSignature: string;
}

/** Polling continues while a rebuild runs or any file is being processed. */
export function shouldPoll(status: Pick<EmbeddingStatus, "reindexRunning" | "activeCount"> | null): boolean {
  return status !== null && (status.reindexRunning || status.activeCount > 0);
}

/**
 * Whether the poller goes on after a silent refresh. An answer that was
 * replaced by a newer load (null) says nothing, so the last known status
 * decides; otherwise a manual refresh in the middle of a tick would end the
 * polling for good.
 */
export function keepPolling(answer: EmbeddingStatus | null, last: Pick<EmbeddingStatus, "reindexRunning" | "activeCount"> | null): boolean {
  return shouldPoll(answer ?? last);
}

/** Appends a page without duplicates; `added` is false when nothing new arrived (paging stops). */
export function appendUnique<T, K>(existing: readonly T[], page: readonly T[], key: (item: T) => K): { items: T[]; added: boolean } {
  const seen = new Set(existing.map(key));
  const fresh = page.filter((item) => {
    const k = key(item);
    if (seen.has(k)) return false;
    seen.add(k);
    return true;
  });
  return { items: [...existing, ...fresh], added: fresh.length > 0 };
}
