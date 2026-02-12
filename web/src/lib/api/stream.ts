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
