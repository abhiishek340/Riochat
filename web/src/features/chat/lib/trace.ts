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

/** Walks a tool payload with hard limits and collects links, images and code results. */
export function analyzeToolPayload(value: unknown): ToolAnalysis {
  const out: ToolAnalysis = { sources: [], images: [], exitCode: null, code: "", text: "" };
  let nodes = 0;
  const seenUrls = new Set<string>();
  const visit = (v: unknown, depth: number): void => {
    if (nodes++ >= MAX_NODES || depth > MAX_DEPTH) return;
    if (typeof v === "string") {
      const t = v.trim();
      if ((t.startsWith("{") || t.startsWith("[")) && t.length <= MAX_EMBEDDED_JSON) {
        const parsed = parseJson(t);
        if (parsed !== undefined) visit(parsed, depth + 1);
      } else if (isImageSource(t) && out.images.length < MAX_RESULTS) {
        out.images.push(t);
      }
      return;
    }
    if (Array.isArray(v)) {
      for (const item of v) visit(item, depth + 1);
      return;
    }
    if (!isRecord(v)) return;
    const url = s(pick(v, "url", "link", "href", "source_url"));
    if (url && isHttpUrl(url) && !seenUrls.has(url) && out.sources.length < MAX_RESULTS) {
      seenUrls.add(url);
      out.sources.push({
        title: s(pick(v, "title", "name", "page_title")) || url,
        url,
        snippet: s(pick(v, "snippet", "description", "content", "text", "summary")).slice(0, 400),
      });
    }
    const exit = pick(v, "exit_code", "exitCode", "returncode");
    if (typeof exit === "number" && out.exitCode === null) out.exitCode = exit;
    const stdout = pick(v, "stdout", "output_text", "result");
    if (typeof stdout === "string" && !out.text) out.text = stdout;
    const code = pick(v, "code", "source_code");
    if (typeof code === "string" && !out.code && code.includes("\n")) out.code = code;
    for (const [key, child] of Object.entries(v)) {
      if (key === "url" || key === "link" || key === "href") continue;
      visit(child, depth + 1);
    }
  };
  visit(value, 0);
  return out;
}

function previewOf(v: unknown): string {
  if (v === undefined || v === null) return "";
  if (typeof v === "string") return v;
  try {
    return JSON.stringify(v, null, 2);
  } catch {
    return "";
  }
}

export function parseToolCalls(event: TraceEventDTO): ToolCallView[] {
  const payload = parseJson(event.payloadJSON);
  if (!isRecord(payload)) return [];
  const list = payload.tool_calls ?? payload.toolCalls;
  if (!Array.isArray(list)) return [];
  const out: ToolCallView[] = [];
  list.forEach((raw, index) => {
    if (!isRecord(raw)) return;
    const id = s(pick(raw, "tool_call_id", "id", "call_id"));
    const name = s(raw.name);
    const type = s(raw.type);
    const outputRaw = pick(raw, "output", "output_detail", "output_text", "output_preview");
    const analysis = analyzeToolPayload(outputRaw);
    const input = previewOf(pick(raw, "input", "input_detail", "input_preview"));
    const rawStatus = s(raw.status);
    out.push({
      id: id || `${event.eventID}#${index}`,
      detailRunId: s(pick(raw, "detail_run_id", "detailRunId")),
      name,
      type,
      rawStatus,
      status: toolStatusOf(rawStatus),
      latencyMs: n(pick(raw, "latency_ms", "latencyMs")),
      error: previewOf(raw.error),
      input,
      output: previewOf(outputRaw),
      category: toolCategory(name, type, analysis.sources.length > 0),
      sources: analysis.sources,
      images: analysis.images,
      exitCode: analysis.exitCode,
      code: analysis.code,
      inputSize: n(pick(raw, "input_size", "inputSize")),
    });
  });
  return out;
}

/** Merges calls with the same id (else kind + input), keeping the more advanced status. */
export function dedupeToolCalls(calls: readonly ToolCallView[]): ToolCallView[] {
  const map = new Map<string, ToolCallView>();
  for (const call of calls) {
    const key = call.id && !call.id.includes("#") ? call.id : `${call.name || call.type}|${call.input.slice(0, 120)}`;
    const prev = map.get(key);
    if (!prev) {
      map.set(key, call);
      continue;
    }
    const better = statusRank(call.rawStatus) >= statusRank(prev.rawStatus) ? call : prev;
    const other = better === call ? prev : call;
    map.set(key, {
      ...better,
      input: better.input || other.input,
      output: better.output || other.output,
      sources: better.sources.length ? better.sources : other.sources,
      images: better.images.length ? better.images : other.images,
      latencyMs: better.latencyMs || other.latencyMs,
      detailRunId: better.detailRunId || other.detailRunId,
      error: better.error || other.error,
    });
  }
  return [...map.values()];
}

// --- agent rounds ------------------------------------------------------------------------

export interface ThinkView {
  id: string;
  title: string;
  summary: string;
  text: string;
  status: string;
  startedAt: string;
  endedAt: string;
}

export interface Round {
  id: string;
  seq: number;
  thinking: ThinkView | null;
  tools: ToolCallView[];
}

export interface LiveThinkEntry {
  id: string;
  roundId: string;
  title: string;
  summary: string;
  text: string;
  status: string;
  startedAt: string;
  endedAt: string;
}

export function eventKind(ev: Pick<TraceEventDTO, "stage" | "phase" | "eventType">): "think" | "tool" | "other" {
  const text = `${ev.stage ?? ""} ${ev.phase ?? ""} ${ev.eventType ?? ""}`.toLowerCase();
  if (text.includes("think") || text.includes("reason")) return "think";
  if (text.includes("tool")) return "tool";
  return "other";
}

function joinUnique(parts: string[]): string {
  const seen: string[] = [];
  for (const p of parts) {
    const t = p.trim();
    if (!t) continue;
    if (seen.some((x) => x === t || x.includes(t))) continue;
    const idx = seen.findIndex((x) => t.includes(x));
    if (idx >= 0) seen[idx] = t;
    else seen.push(t);
  }
  return seen.join("\n\n");
}

/** Groups think and tool events into rounds ordered by sequence. */
export function buildRounds(trace: ProcessTraceDTO | null | undefined, live: readonly LiveThinkEntry[] = []): Round[] {
  const events = [...(trace?.events ?? [])].sort((a, b) => (a.seq ?? 0) - (b.seq ?? 0));
  const rounds = new Map<string, Round & { texts: string[] }>();
  const order: string[] = [];
  const touch = (key: string, seq: number) => {
    let r = rounds.get(key);
    if (!r) {
      r = { id: key, seq, thinking: null, tools: [], texts: [] };
      rounds.set(key, r);
      order.push(key);
    }
    return r;
  };
  const byEvent = new Map<string, string>();

  for (const ev of events) {
    const kind = eventKind(ev);
    if (kind === "think") {
      const key = ev.roundID || ev.eventID;
      const r = touch(key, ev.seq ?? 0);
      byEvent.set(ev.eventID, key);
      if (ev.contentMarkdown) r.texts.push(ev.contentMarkdown);
      const prev = r.thinking;
      r.thinking = {
        id: key,
        title: ev.title || prev?.title || "",
        summary: ev.summary || prev?.summary || "",
        text: "",
        status: ev.status || prev?.status || "",
        startedAt: prev?.startedAt || ev.startedAt || "",
        endedAt: ev.endedAt || prev?.endedAt || "",
      };
    } else if (kind === "tool") {
      const key = (ev.roundID && rounds.has(ev.roundID) ? ev.roundID : "") || (ev.parentEventID && byEvent.get(ev.parentEventID)) || ev.roundID || ev.eventID;
      const r = touch(key, ev.seq ?? 0);
      byEvent.set(ev.eventID, key);
      r.tools.push(...parseToolCalls(ev));
    }
  }

  // Live thinking enriches rounds it matches and creates the ones it does not.
  for (const entry of live) {
    const key = entry.roundId || entry.id;
    const r = touch(key, Number.MAX_SAFE_INTEGER);
    if (entry.text) r.texts.push(entry.text);
    const prev = r.thinking;
    r.thinking = {
      id: key,
      title: entry.title || prev?.title || "",
      summary: entry.summary || prev?.summary || "",
      text: "",
      status: entry.status || prev?.status || "",
      startedAt: prev?.startedAt || entry.startedAt,
      endedAt: entry.endedAt || prev?.endedAt || "",
    };
  }

  // Blocks of the trace itself (a snapshot with no events).
  const think = trace?.upstreamThink;
  if (rounds.size === 0 && think && (think.contentMarkdown || think.summary)) {
    const r = touch(think.roundID || "think", 0);
    r.texts.push(think.contentMarkdown ?? "");
    r.thinking = {
      id: r.id, title: think.title ?? "", summary: think.summary ?? "", text: "", status: think.status ?? "",
      startedAt: think.startedAt ?? "", endedAt: "",
    };
  }

  return order.map((key) => {
    const r = rounds.get(key) as Round & { texts: string[] };
    return {
      id: r.id,
      seq: r.seq,
      thinking: r.thinking ? { ...r.thinking, text: joinUnique(r.texts) } : null,
      tools: dedupeToolCalls(r.tools),
    };
  });
}

export function roundTotals(rounds: readonly Round[]): { toolCalls: number; thinkRounds: number } {
  return {
    toolCalls: rounds.reduce((total, r) => total + r.tools.length, 0),
    thinkRounds: rounds.filter((r) => r.thinking && (r.thinking.text || r.thinking.summary)).length,
  };
}

// --- merging live updates -------------------------------------------------------------------

function mergeEvent(prev: TraceEventDTO | undefined, next: TraceEventDTO): TraceEventDTO {
  if (!prev) return next;
  const longer = (prev.contentMarkdown ?? "").length > (next.contentMarkdown ?? "").length;
  return {
    ...next,
    contentMarkdown: longer ? prev.contentMarkdown : next.contentMarkdown,
    summary: next.summary || prev.summary,
    endedAt: next.endedAt ?? prev.endedAt,
  };
}

/** A full snapshot replaces the trace, but thinking keeps text/time it already had. */
export function mergeTrace(prev: ProcessTraceDTO | null | undefined, next: ProcessTraceDTO | null | undefined): ProcessTraceDTO | null {
  if (!next) return prev ?? null;
  if (!prev) return next;
  const prevEvents = new Map((prev.events ?? []).map((e) => [e.eventID, e] as const));
  const events = (next.events ?? []).map((e) => (eventKind(e) === "think" ? mergeEvent(prevEvents.get(e.eventID), e) : e));
  const upstream = next.upstreamThink
    ? {
        ...next.upstreamThink,
        contentMarkdown:
          (prev.upstreamThink?.contentMarkdown ?? "").length > (next.upstreamThink.contentMarkdown ?? "").length
            ? prev.upstreamThink?.contentMarkdown
            : next.upstreamThink.contentMarkdown,
        summary: next.upstreamThink.summary || prev.upstreamThink?.summary,
      }
    : prev.upstreamThink;
  return { ...prev, ...next, events, upstreamThink: upstream };
}

// --- live thinking entries -------------------------------------------------------------------

export function terminalThinkStatus(status: string | undefined): boolean {
  const v = (status ?? "").toLowerCase();
  return v === "completed" || v === "error" || v === "done" || v === "failed";
}

export function thinkKey(ev: { roundID?: string; eventID?: string }): string {
  return ev.roundID || ev.eventID || "think";
}

/** Appends text to (or replaces text of) the entry with this key. */
export function applyThinkText(entries: readonly LiveThinkEntry[], key: string, text: string, mode: "append" | "replace", meta: Partial<LiveThinkEntry> = {}): LiveThinkEntry[] {
  const at = entries.findIndex((e) => e.id === key);
  if (at < 0) {
    return [
      ...entries,
      { id: key, roundId: meta.roundId ?? key, title: meta.title ?? "", summary: meta.summary ?? "", text, status: meta.status ?? "running", startedAt: meta.startedAt ?? "", endedAt: meta.endedAt ?? "" },
    ];
  }
  const cur = entries[at] as LiveThinkEntry;
  const nextText = mode === "append" ? cur.text + text : text.length >= cur.text.length ? text : cur.text;
  const merged: LiveThinkEntry = {
    ...cur,
    text: nextText,
    title: meta.title || cur.title,
    summary: meta.summary || cur.summary,
    status: meta.status || cur.status,
    startedAt: cur.startedAt || meta.startedAt || "",
    endedAt: meta.endedAt || cur.endedAt,
  };
  const copy = [...entries];
  copy[at] = merged;
  return copy;
}
