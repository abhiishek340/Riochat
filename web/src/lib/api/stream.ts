/**
 * Streaming helpers.
 *
 * Two wire formats exist on the backend:
 *  - generation streams (`.../messages/stream`, media, temporary chat, run
 *    resume) send NDJSON-like output: JSON documents back to back;
 *  - the active-run notification stream is Server-Sent Events.
 * Both are consumed from a fetch Response body, so the access token travels in
 * a normal Authorization header (EventSource cannot do that).
 */
import { ApiError, StreamProtocolError, isAbortError } from "./errors";
import type { RequestOptions } from "./http";
import { api } from "./client";
import { isRecord } from "../utils/guards";

// --- JSON document splitter --------------------------------------------------

/**
 * Cuts complete top-level `{...}` documents out of arbitrarily chunked text by
 * counting brace depth outside strings. Whitespace between documents is
 * skipped; any other leading character is a protocol error.
 */
export class JsonDocumentSplitter {
  private buf = "";
  private depth = 0;
  private inString = false;
  private escaped = false;
  private start = -1;
  private scanned = 0;

  push(chunk: string): string[] {
    this.buf += chunk;
    const out: string[] = [];
    for (let i = this.scanned; i < this.buf.length; i++) {
      const ch = this.buf[i] as string;
      if (this.depth === 0) {
        if (ch === " " || ch === "\n" || ch === "\r" || ch === "\t") continue;
        if (ch !== "{") throw new StreamProtocolError(`unexpected character in stream: ${JSON.stringify(ch)}`);
        this.start = i;
        this.depth = 1;
        this.inString = false;
        this.escaped = false;
        continue;
      }
      if (this.inString) {
        if (this.escaped) this.escaped = false;
        else if (ch === "\\") this.escaped = true;
        else if (ch === '"') this.inString = false;
        continue;
      }
      if (ch === '"') this.inString = true;
      else if (ch === "{") this.depth++;
      else if (ch === "}") {
        this.depth--;
        if (this.depth === 0) {
          out.push(this.buf.slice(this.start, i + 1));
          this.start = -1;
        }
      }
    }
    // Drop what is consumed; keep an unfinished document for the next chunk.
    if (this.depth === 0) {
      this.buf = "";
      this.scanned = 0;
    } else {
      this.buf = this.buf.slice(this.start);
      this.scanned = this.buf.length;
      this.start = 0;
    }
    return out;
  }

  /** True when bytes of an unfinished document remain. */
  get pending(): boolean {
    return this.depth > 0;
  }
}

async function* decodeChunks(response: Response): AsyncGenerator<string> {
  if (!response.body) return;
  const reader = response.body.getReader();
  const decoder = new TextDecoder("utf-8");
  try {
    for (;;) {
      const { done, value } = await reader.read();
      if (done) break;
      if (value) yield decoder.decode(value, { stream: true });
    }
    const tail = decoder.decode();
    if (tail) yield tail;
  } finally {
    reader.releaseLock();
  }
}

export async function* readJsonDocuments(response: Response): AsyncGenerator<unknown> {
  const splitter = new JsonDocumentSplitter();
  for await (const chunk of decodeChunks(response)) {
    for (const doc of splitter.push(chunk)) {
      try {
        yield JSON.parse(doc);
      } catch {
        throw new StreamProtocolError("malformed JSON document in stream");
      }
    }
  }
  if (splitter.pending) throw new StreamProtocolError("stream ended inside a document");
}

// --- conversation events -----------------------------------------------------

export interface TraceFields {
  [key: string]: unknown;
}

export type StreamEvent =
  | { type: "file_proc" | "rag_search"; message: string; seq?: number }
  | { type: "process_update" | "upstream_think_delta"; seq?: number; [key: string]: unknown }
  | { type: "delta"; delta: string; replace?: boolean; seq?: number }
  | { type: "usage"; seq?: number; [key: string]: unknown }
  | { type: "media_status" | "media_image_delta" | "compact_done" | "moderation_checking"; seq?: number; [key: string]: unknown }
  | { type: "moderation_blocked"; eventID?: string; direction?: string; categories?: string[]; billedReason?: string; seq?: number }
  | { type: "completed"; data: unknown; seq?: number }
  | { type: "error"; status?: number; message: string; errorCode?: string; requestId?: string; debug?: unknown; data?: unknown; seq?: number };

const KNOWN = new Set([
  "file_proc", "rag_search", "process_update", "upstream_think_delta", "delta", "usage", "media_status",
  "media_image_delta", "compact_done", "moderation_checking", "moderation_blocked", "completed", "error",
]);

export interface ConversationStreamHandlers {
  /** Every event, in order. Unknown types arrive here too before the call fails. */
  onEvent?: (event: StreamEvent) => void;
  onDelta?: (text: string) => void;
  /** A `delta` with `replace`: the text so far is replaced wholesale. */
  onSnapshot?: (text: string) => void;
  /** Reported for every event carrying a positive `seq`, so callers can resume. */
  onSeq?: (seq: number) => void;
  /** An `error` event that still carried a result: the run was interrupted but persisted. */
  onInterrupted?: (data: unknown, error: Extract<StreamEvent, { type: "error" }>) => void;
}

export interface ConsumeOptions {
  /** Resume streams may legitimately end without a completion. */
  resume?: boolean;
  status?: number;
}

/**
 * Reads a generation stream to its end and applies the outcome rules:
 * moderation block -> ApiError `content_moderation.blocked`; completion ->
 * its data; terminal error -> interruption callback (with data) or ApiError;
 * otherwise an error about the missing final payload.
 */
export async function consumeConversationStream<T = unknown>(
  response: Response,
  handlers: ConversationStreamHandlers = {},
  options: ConsumeOptions = {},
): Promise<T | undefined> {
  const status = options.status ?? response.status;
  if (!response.body) {
    if (options.resume) return undefined;
    throw new ApiError({ message: "Stream body is empty", status });
  }
  let completed: { data: unknown } | null = null;
  let blocked: Extract<StreamEvent, { type: "moderation_blocked" }> | null = null;

  for await (const raw of readJsonDocuments(response)) {
    if (!isRecord(raw) || typeof raw.type !== "string") throw new StreamProtocolError("stream event without a type");
    const event = raw as StreamEvent;
    handlers.onEvent?.(event);
    if (typeof event.seq === "number" && event.seq > 0) handlers.onSeq?.(event.seq);

    if (!KNOWN.has(event.type)) {
      throw new ApiError({ message: `Unknown stream event: ${event.type}`, status });
    }
    switch (event.type) {
      case "delta":
        if (event.replace === true) handlers.onSnapshot?.(event.delta ?? "");
        else handlers.onDelta?.(event.delta ?? "");
        break;
      case "moderation_blocked":
        blocked = event;
        break;
      case "completed":
        completed = { data: event.data };
        break;
      case "error": {
        if (event.data !== undefined && event.data !== null) {
          handlers.onInterrupted?.(event.data, event);
          return event.data as T;
        }
        throw new ApiError({
          message: event.message || "Stream failed",
          status: typeof event.status === "number" ? event.status : status,
          errorCode: event.errorCode,
          requestId: event.requestId,
          details: event.debug,
        });
      }
      default:
        break;
    }
  }

  if (blocked) {
    throw new ApiError({
      message: "Content blocked by moderation",
      status,
      errorCode: "content_moderation.blocked",
      details: { eventID: blocked.eventID, direction: blocked.direction, categories: blocked.categories },
    });
  }
  if (completed) return completed.data as T;
  if (options.resume) return undefined;
  throw new ApiError({ message: "Stream completed without final payload", status });
}

export interface StreamPostOptions extends ConversationStreamHandlers {
  signal?: AbortSignal;
  query?: RequestOptions["query"];
}

/** POST a JSON body (or FormData) to a generation endpoint and consume its stream. */
export async function postConversationStream<T = unknown>(
  path: string,
  body: unknown,
  options: StreamPostOptions = {},
): Promise<T | undefined> {
  const { signal, query, ...handlers } = options;
  const response = await api.raw(path, {
    method: "POST",
    body,
    signal,
    query,
    headers: { Accept: "application/x-ndjson" },
  });
  return consumeConversationStream<T>(response, handlers);
}

/** Re-attach to a run: GET /conversation-runs/{id}/stream?snapshot=true[&after=seq]. */
export async function resumeRunStream<T = unknown>(
  runID: string,
  options: StreamPostOptions & { after?: number } = {},
): Promise<T | undefined> {
  const { signal, after, ...handlers } = options;
  const response = await api.raw(`/conversation-runs/${encodeURIComponent(runID)}/stream`, {
    signal,
    query: { snapshot: "true", after: after && after > 0 ? Math.floor(after) : undefined },
    headers: { Accept: "application/x-ndjson" },
  });
  return consumeConversationStream<T>(response, handlers, { resume: true });
}

// --- Server-Sent Events ------------------------------------------------------

/**
 * Yields the joined `data:` payload of each SSE frame. Frames end at a blank
 * line (CRLF or LF); comment lines and empty frames are skipped. A buffered
 * remainder is processed when the stream ends.
 */
export async function* readSseData(response: Response): AsyncGenerator<string> {
  let buffer = "";
  const flush = function* (frame: string): Generator<string> {
    const lines: string[] = [];
    for (const line of frame.split(/\r?\n/)) {
      if (line.startsWith("data:")) lines.push(line.slice(5).trimStart());
    }
    const data = lines.join("\n").trim();
    if (data) yield data;
  };
  for await (const chunk of decodeChunks(response)) {
    buffer += chunk;
    for (;;) {
      const match = /\r?\n\r?\n/.exec(buffer);
      if (!match) break;
      const frame = buffer.slice(0, match.index);
      buffer = buffer.slice(match.index + match[0].length);
      yield* flush(frame);
    }
  }
  if (buffer.trim()) yield* flush(buffer);
}

export interface ActiveRun {
  runID: string;
  conversationPublicID: string;
}

export type ActiveRunEvent =
  | { type: "snapshot"; runs: ActiveRun[] }
  | { type: "started" | "finished"; runID: string; conversationPublicID?: string };

/** Parses one SSE payload; malformed or unrecognised events yield null so the connection survives. */
export function parseActiveRunEvent(data: string): ActiveRunEvent | null {
  let value: unknown;
  try {
    value = JSON.parse(data);
  } catch {
    return null;
  }
  if (!isRecord(value)) return null;
  if (value.type === "snapshot") {
    if (!Array.isArray(value.runs)) return null;
    const runs: ActiveRun[] = [];
    for (const item of value.runs) {
      if (!isRecord(item) || typeof item.runID !== "string" || !item.runID) return null; // discard the whole snapshot
      runs.push({
        runID: item.runID,
        conversationPublicID: typeof item.conversationPublicID === "string" ? item.conversationPublicID : "",
      });
    }
    return { type: "snapshot", runs };
  }
  if ((value.type === "started" || value.type === "finished") && typeof value.runID === "string" && value.runID) {
    return {
      type: value.type,
      runID: value.runID,
      conversationPublicID: typeof value.conversationPublicID === "string" ? value.conversationPublicID : undefined,
    };
  }
  return null;
}

/**
 * Follows GET /conversation-runs/stream until `signal` aborts or the server
 * closes it. Resolves when the stream ends; callers decide about reconnecting.
 */
export async function followActiveRuns(onEvent: (event: ActiveRunEvent) => void, signal: AbortSignal): Promise<void> {
  const response = await api.raw("/conversation-runs/stream", { signal, headers: { Accept: "text/event-stream" } });
  if (!response.body) throw new ApiError({ message: "Active-run stream is unavailable", status: response.status });
  try {
    for await (const data of readSseData(response)) {
      const event = parseActiveRunEvent(data);
      if (event) onEvent(event);
    }
  } catch (error) {
    if (!isAbortError(error)) throw error;
  }
}
