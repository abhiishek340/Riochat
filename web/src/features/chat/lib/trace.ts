/**
 * Process traces: what a reply did before and while it answered. The server
 * sends a trace object per message (summary, thinking events, tool calls,
 * prompt assembly); this module turns it into display models and merges live
 * updates into it.
 */
import { isRecord } from "@/lib/utils/guards";
import type { KnowledgeSource, ProcessTraceDTO, TraceBlockDTO, TraceEventDTO } from "../types";

// --- small readers ------------------------------------------------------------------

function parseJson(text: string | undefined): unknown {
  if (!text) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

function s(v: unknown): string {
  return typeof v === "string" ? v : typeof v === "number" ? String(v) : "";
}

function n(v: unknown): number {
  return typeof v === "number" && Number.isFinite(v) ? v : 0;
}

function pick(rec: Record<string, unknown>, ...names: string[]): unknown {
  for (const name of names) if (rec[name] !== undefined && rec[name] !== null) return rec[name];
  return undefined;
}

// --- stages (process summary) -----------------------------------------------------

export type StageKind = "context_planning" | "content_retrieval" | "file_context" | "context_compaction" | "skill_context" | "other";
export type FileGroup = "direct_images" | "native" | "adaptive" | "retrieval" | "full_context" | "skipped" | "unsupported" | "other";

export interface StageFile {
  name: string;
  fileId: string;
  group: FileGroup;
}

export interface TraceStage {
  kind: StageKind;
  status: string;
  label: string;
  detail: string;
  files: StageFile[];
  includedCount: number;
  skippedCount: number;
  retrieval?: { fileCount: number; chunkCount: number; fallback: string; status: string };
  compaction?: { status: string; fromTurn: number; toTurn: number; sourceTokens: number; summaryTokens: number };
  citations: KnowledgeSource[];
}

const KINDS: StageKind[] = ["context_planning", "content_retrieval", "file_context", "context_compaction", "skill_context"];
const GROUPS: FileGroup[] = ["direct_images", "native", "adaptive", "retrieval", "full_context", "skipped", "unsupported"];
const SKIPPED_GROUPS = new Set<FileGroup>(["skipped", "unsupported"]);

function stageKind(v: unknown): StageKind {
  const k = s(v);
  return (KINDS as string[]).includes(k) ? (k as StageKind) : "other";
}

function readFiles(rec: Record<string, unknown>): StageFile[] {
  const out: StageFile[] = [];
  const groups = rec.groups;
  if (isRecord(groups)) {
    for (const g of GROUPS) {
      const list = groups[g];
      if (!Array.isArray(list)) continue;
      for (const f of list) {
        if (typeof f === "string") out.push({ name: f, fileId: "", group: g });
        else if (isRecord(f)) out.push({ name: s(pick(f, "file_name", "fileName", "name")), fileId: s(pick(f, "file_id", "fileId")), group: g });
      }
    }
  }
  if (Array.isArray(rec.files)) {
    for (const f of rec.files) {
      if (!isRecord(f)) continue;
      const g = s(pick(f, "group", "mode", "status"));
      out.push({
        name: s(pick(f, "file_name", "fileName", "name")),
        fileId: s(pick(f, "file_id", "fileId")),
        group: (GROUPS as string[]).includes(g) ? (g as FileGroup) : "other",
      });
    }
  }
  return out;
}

export function readCitations(list: unknown): KnowledgeSource[] {
  if (!Array.isArray(list)) return [];
  const out: KnowledgeSource[] = [];
  for (const c of list) {
    if (!isRecord(c)) continue;
    out.push({
      fileName: s(pick(c, "file_name", "fileName")),
      fileId: s(pick(c, "file_id", "fileId")),
      chunkIndex: n(pick(c, "chunk_index", "chunkIndex")),
      score: n(c.score),
      preview: s(c.preview),
      modality: c.modality === "image" ? "image" : "text",
    });
  }
  return out;
}

function stageFrom(rec: Record<string, unknown>): TraceStage {
  const kind = stageKind(pick(rec, "kind", "stage", "type"));
  const files = readFiles(rec);
  const included = files.filter((f) => !SKIPPED_GROUPS.has(f.group)).length;
  const skipped = files.filter((f) => SKIPPED_GROUPS.has(f.group)).length;
  const stage: TraceStage = {
    kind,
    status: s(rec.status),
    label: s(pick(rec, "label", "title", "name")),
    detail: s(pick(rec, "detail", "summary", "message")),
    files,
    includedCount: files.length > 0 ? included : n(pick(rec, "included", "included_count")),
    skippedCount: files.length > 0 ? skipped : n(pick(rec, "skipped_count", "skipped")),
    citations: readCitations(rec.citations),
  };
  if (kind === "content_retrieval") {
    stage.retrieval = {
      fileCount: n(pick(rec, "file_count", "fileCount")),
      chunkCount: n(pick(rec, "chunk_count", "chunkCount")),
      fallback: s(rec.fallback),
      status: s(rec.status),
    };
  }
  if (kind === "context_compaction") {
    stage.compaction = {
      status: s(rec.status),
      fromTurn: n(pick(rec, "from_turn", "fromTurn")),
      toTurn: n(pick(rec, "to_turn", "toTurn")),
      sourceTokens: n(pick(rec, "source_tokens", "sourceTokens")),
      summaryTokens: n(pick(rec, "summary_tokens", "summaryTokens")),
    };
  }
  return stage;
}

const LEGACY_LINE = /^\*\*(.+?)\*\*\s*[:\uff1a]\s*(.*)$/;

/** Stages from the payload's `trace_stages`, else legacy `**label**: detail` lines. */
export function parseStages(process: TraceBlockDTO | null | undefined): TraceStage[] {
  if (!process) return [];
  const payload = parseJson(process.payloadJSON);
  const raw = isRecord(payload) ? payload.trace_stages ?? payload.traceStages : undefined;
  if (Array.isArray(raw)) return raw.filter(isRecord).map(stageFrom);
  const out: TraceStage[] = [];
  for (const line of (process.contentMarkdown ?? "").split(/\r?\n/)) {
    const m = LEGACY_LINE.exec(line.trim());
    if (m) out.push({ kind: "other", status: "", label: m[1] ?? "", detail: m[2] ?? "", files: [], includedCount: 0, skippedCount: 0, citations: [] });
  }
  return out;
}

/** Stages that merely restate a failure or an unsupported protocol are noise. */
export function isNoiseStage(stage: TraceStage): boolean {
  const text = `${stage.label} ${stage.detail}`.toLowerCase();
  if (stage.kind === "context_planning" && /(does not support|not support).*(stateful|continuation|branch)/.test(text)) return true;
  if (stage.kind === "other" && /upstream request failed/.test(text)) return true;
  return false;
}

/** Retrieval citations taken from the stage payloads. */
export function stageCitations(trace: ProcessTraceDTO | null | undefined): KnowledgeSource[] {
  const stages = parseStages(trace?.process);
  const events = trace?.events ?? [];
  const out: KnowledgeSource[] = stages.flatMap((st) => st.citations);
  for (const ev of events) {
    const payload = parseJson(ev.payloadJSON);
    if (isRecord(payload) && Array.isArray(payload.citations)) out.push(...readCitations(payload.citations));
  }
  return out;
}

/** "Used N sources": distinct files by id, else by name. */
export function distinctSourceCount(sources: readonly KnowledgeSource[]): number {
  return new Set(sources.map((x) => x.fileId || x.fileName)).size;
}

export type StatelessReason = "missing_stored_fingerprint" | "missing_current_fingerprint" | "prompt_fingerprint_mismatch" | "previous_response_rejected";

export function reasonForStateless(reason: string | undefined): StatelessReason | null {
  const known: StatelessReason[] = ["missing_stored_fingerprint", "missing_current_fingerprint", "prompt_fingerprint_mismatch", "previous_response_rejected"];
  return known.find((k) => k === reason) ?? null;
}

// --- tool calls -------------------------------------------------------------------------

export type ToolStatus = "calling" | "completed" | "reused" | "failed";
export type ToolCategory = "web_search" | "code" | "image" | "shell" | "generic";

export interface SourceLink {
  title: string;
  url: string;
  snippet: string;
}

export interface ToolCallView {
  id: string;
  detailRunId: string;
  name: string;
  type: string;
  rawStatus: string;
  status: ToolStatus;
  latencyMs: number;
  error: string;
  input: string;
  output: string;
  category: ToolCategory;
  sources: SourceLink[];
  images: string[];
  exitCode: number | null;
  code: string;
  inputSize: number;
}

const ACTIVE = new Set(["requested", "streaming", "queued", "in_progress", "searching", "calling", "running", "pending"]);

export function toolStatusOf(raw: string): ToolStatus {
  const v = raw.toLowerCase();
  if (v === "error" || v === "failed") return "failed";
  if (v === "reused") return "reused";
  if (v === "success" || v === "completed") return "completed";
  if (ACTIVE.has(v) || v === "") return "calling";
  return "calling";
}

function statusRank(raw: string): number {
  const v = raw.toLowerCase();
  if (v === "error" || v === "failed") return 4;
  if (v === "success" || v === "completed" || v === "reused") return 3;
  if (ACTIVE.has(v)) return 2;
  return 1;
}

const SEARCH_NAME = /(search|browse|crawl|reader|fetch|scrape|web_)/i;
const CODE_NAME = /(code|python|execute|interpreter|sandbox|notebook)/i;
const IMAGE_NAME = /(image_gen|generate_image|image_generation|draw|dall)/i;
const SHELL_NAME = /(shell|bash|terminal|command|cmd)/i;

export function toolCategory(name: string, type: string, hasSources: boolean): ToolCategory {
  const key = `${name} ${type}`;
  if (IMAGE_NAME.test(key)) return "image";
  if (SHELL_NAME.test(key)) return "shell";
  if (CODE_NAME.test(key)) return "code";
  if (SEARCH_NAME.test(key) || hasSources) return "web_search";
  return "generic";
}

const MAX_DEPTH = 24;
const MAX_NODES = 512;
const MAX_RESULTS = 64;
const MAX_EMBEDDED_JSON = 64 * 1024;

export function isHttpUrl(value: string): boolean {
  return /^https?:\/\/[^\s]+$/i.test(value.trim());
}

const IMG_DATA = /^data:image\/(png|jpe?g|webp|gif);base64,/i;
const RAW_B64 = /^[A-Za-z0-9+/]{200,}={0,2}$/;

export function isImageSource(value: string): boolean {
  const v = value.trim();
  if (/^https?:\/\//i.test(v)) return /\.(png|jpe?g|webp|gif)(\?|#|$)/i.test(v) || /\/files\/[^/]+\/content/.test(v);
  return v.startsWith("blob:") || IMG_DATA.test(v) || RAW_B64.test(v);
}

export interface ToolAnalysis {
  sources: SourceLink[];
  images: string[];
  exitCode: number | null;
  code: string;
  text: string;
}
