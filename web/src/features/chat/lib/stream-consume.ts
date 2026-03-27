/**
 * Reads a generation stream (send, media, temporary chat or resume) and turns
 * its events into callbacks. The shared client has a consumer too, but the
 * resume endpoint also sends a `snapshot` event (answer and thinking text so
 * far), which the chat area needs.
 */
import { ApiError } from "@/lib/api/errors";
import { readJsonDocuments } from "@/lib/api/stream";
import { isRecord } from "@/lib/utils/guards";
import type { SendResultDTO } from "../types";

export interface UsageEvent {
  input_tokens?: number;
  output_tokens?: number;
  cache_read_tokens?: number;
  cache_write_tokens?: number;
  reasoning_tokens?: number;
}

export interface ThinkEvent {
  status?: string;
  title?: string;
  summary?: string;
  stage?: string;
  roundID?: string;
  eventID?: string;
  startedAt?: string;
  endedAt?: string;
  kind?: string;
  delta?: string;
  contentMarkdown?: string;
  block?: unknown;
  trace?: unknown;
  reasoning?: unknown;
}

export interface ProcessEvent {
  status?: string;
  block?: unknown;
  trace?: unknown;
}

export interface MediaStatusEvent {
  status: string;
  message?: string;
  content_type?: string;
}

export interface MediaImageEvent {
  index?: number;
  b64_json: string;
  mime_type?: string;
  revised_prompt?: string;
}

export interface BlockedEvent {
  eventID?: string;
  direction?: string;
  categories?: string[];
  billedReason?: string;
  data?: SendResultDTO;
}

export interface CompactEvent {
  method: string;
  freed_tokens: number;
  kept_turns: number;
  summary_preview: string;
}

export interface StreamErrorEvent {
  status?: number;
  message: string;
  errorCode?: string;
  requestId?: string;
  debug?: unknown;
  data?: SendResultDTO;
}

export interface RunStreamHandlers {
  onSeq?: (seq: number) => void;
  onFileProc?: (message: string) => void;
  onRagSearch?: (message: string) => void;
  onProcess?: (event: ProcessEvent) => void;
  onThink?: (event: ThinkEvent) => void;
  onDelta?: (text: string) => void;
  /**
   * Authoritative text (a `delta` with `replace`, or a `snapshot` event). `seq` is the
   * event's sequence number, 0 when it carried none.
   */
  onSnapshot?: (text: string, meta?: { seq: number }) => void;
  onSnapshotThink?: (text: string) => void;
  onUsage?: (usage: UsageEvent) => void;
  onMediaStatus?: (event: MediaStatusEvent) => void;
  onMediaImage?: (event: MediaImageEvent) => void;
  onModerationChecking?: () => void;
  onCompact?: (event: CompactEvent) => void;
}

export type StreamOutcome =
  | { kind: "completed"; data: SendResultDTO }
  | { kind: "interrupted"; data: SendResultDTO; error: StreamErrorEvent }
  | { kind: "blocked"; event: BlockedEvent }
  /** A resumed stream that closed without a terminal event. */
  | { kind: "ended" };

const KNOWN = new Set([
  "file_proc", "rag_search", "process_update", "upstream_think_delta", "delta", "snapshot", "usage", "media_status",
  "media_image_delta", "moderation_checking", "moderation_blocked", "compact_done", "completed", "error",
]);

function str(v: unknown): string {
  return typeof v === "string" ? v : "";
}

function seqOf(raw: Record<string, unknown>): number {
  return typeof raw.seq === "number" && Number.isFinite(raw.seq) && raw.seq > 0 ? raw.seq : 0;
}

function asResult(v: unknown): SendResultDTO | null {
  if (!isRecord(v)) return null;
  return v as unknown as SendResultDTO;
}

/** Fails the way the spec asks: status from the event, else the HTTP status. */
function failure(event: Record<string, unknown>, httpStatus: number, fallback: string, fallbackCode?: string): ApiError {
  return new ApiError({
    message: str(event.message) || fallback,
    status: typeof event.status === "number" ? event.status : httpStatus,
    errorCode: str(event.errorCode) || (str(event.message) ? undefined : fallbackCode),
    requestId: str(event.requestId) || undefined,
    details: event.debug,
  });
}

export async function consumeRunStream(
  response: Response,
  handlers: RunStreamHandlers,
  options: { resume?: boolean } = {},
): Promise<StreamOutcome> {
  if (!response.body) {
    if (options.resume) return { kind: "ended" };
    throw new ApiError({ message: "The reply stream was empty.", status: response.status, errorCode: "chat.stream_empty" });
  }
  let blocked: BlockedEvent | null = null;

  for await (const raw of readJsonDocuments(response)) {
    if (!isRecord(raw) || typeof raw.type !== "string") {
      throw new ApiError({ message: "The reply stream failed.", status: response.status, errorCode: "chat.stream_failed" });
    }
    const type = raw.type;
    const seq = raw.seq;
    if (typeof seq === "number" && Number.isFinite(seq) && seq > 0) handlers.onSeq?.(seq);

    if (!KNOWN.has(type)) throw failure(raw, response.status, "The reply stream failed.", "chat.stream_failed");

    switch (type) {
      case "file_proc":
        handlers.onFileProc?.(str(raw.message));
        break;
      case "rag_search":
        handlers.onRagSearch?.(str(raw.message));
        break;
      case "process_update":
        handlers.onProcess?.(raw as ProcessEvent);
        break;
      case "upstream_think_delta":
        handlers.onThink?.(raw as ThinkEvent);
        break;
      case "delta":
        if (raw.replace === true) handlers.onSnapshot?.(str(raw.delta), { seq: seqOf(raw) });
        else if (typeof raw.delta === "string" && raw.delta) handlers.onDelta?.(raw.delta);
        break;
      case "snapshot":
        handlers.onSnapshot?.(str(raw.content), { seq: seqOf(raw) });
        if (typeof raw.think === "string") handlers.onSnapshotThink?.(raw.think);
        break;
      case "usage":
        handlers.onUsage?.(raw as UsageEvent);
        break;
      case "media_status":
        handlers.onMediaStatus?.({ status: str(raw.status), message: str(raw.message) || undefined, content_type: str(raw.content_type) || undefined });
        break;
      case "media_image_delta":
        if (typeof raw.b64_json === "string") handlers.onMediaImage?.(raw as unknown as MediaImageEvent);
        break;
      case "moderation_checking":
        handlers.onModerationChecking?.();
        break;
      case "compact_done":
        handlers.onCompact?.(raw as unknown as CompactEvent);
        break;
      case "moderation_blocked":
        blocked = {
          eventID: str(raw.eventID) || undefined,
          direction: str(raw.direction) || undefined,
          categories: Array.isArray(raw.categories) ? raw.categories.filter((c): c is string => typeof c === "string") : undefined,
          billedReason: str(raw.billedReason) || undefined,
          data: asResult(raw.data) ?? undefined,
        };
        break;
      case "completed": {
        const data = asResult(raw.data);
        if (!data) throw new ApiError({ message: "The reply ended without a result.", status: response.status, errorCode: "chat.stream_no_result" });
        return { kind: "completed", data };
      }
      case "error": {
        const data = asResult(raw.data);
        if (data) {
          return {
            kind: "interrupted",
            data,
            error: {
              status: typeof raw.status === "number" ? raw.status : undefined,
              message: str(raw.message),
              errorCode: str(raw.errorCode) || undefined,
              requestId: str(raw.requestId) || undefined,
              debug: raw.debug,
              data,
            },
          };
        }
        throw failure(raw, response.status, "The reply stream failed.", "chat.stream_failed");
      }
    }
    if (blocked && type === "moderation_blocked") {
      // The blocked event is terminal; the server closes the stream after it.
      return { kind: "blocked", event: blocked };
    }
  }

  if (blocked) return { kind: "blocked", event: blocked };
  if (options.resume) return { kind: "ended" };
  throw new ApiError({ message: "The reply ended without a result.", status: response.status, errorCode: "chat.stream_no_result" });
}
