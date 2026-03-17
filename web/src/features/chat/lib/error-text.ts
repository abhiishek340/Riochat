/**
 * Short, readable summaries of upstream failures.
 */
import { isRecord } from "@/lib/utils/guards";
import type { DebugSnapshot } from "../types";

function structuredMessage(value: unknown, depth = 0): string {
  if (depth > 4 || !isRecord(value)) return "";
  const error = value.error;
  if (isRecord(error)) {
    if (typeof error.message === "string" && error.message.trim()) return error.message.trim();
  } else if (typeof error === "string" && error.trim()) {
    return error.trim();
  }
  if (typeof value.message === "string" && value.message.trim()) return value.message.trim();
  if (isRecord(error) && typeof error.code === "string" && error.code.trim()) return error.code.trim();
  if (typeof value.code === "string" && value.code.trim()) return value.code.trim();
  return structuredMessage(value.response, depth + 1);
}

function tryJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

/** A message from a JSON body, or from the `data:` lines of a streamed one. */
export function reasonFromBody(body: string | undefined): string {
  const text = (body ?? "").trim();
  if (!text) return "";
  if (text.startsWith("{")) return structuredMessage(tryJson(text));
  if (/^data:/m.test(text)) {
    for (const line of text.split(/\r?\n/)) {
      if (!line.startsWith("data:")) continue;
      const data = line.slice(5).trim();
      if (!data || data === "[DONE]") continue;
      const found = structuredMessage(tryJson(data));
      if (found) return found;
    }
  }
  return "";
}

export function isStreamedBody(body: string | undefined): boolean {
  return /^data:/m.test((body ?? "").trim());
}

/** Splits `Details:` off a server message. */
function splitDetails(message: string): { head: string; details: string } {
  const at = message.search(/\bDetails\s*:/i);
  if (at < 0) return { head: message, details: "" };
  return { head: message.slice(0, at), details: message.slice(at).replace(/^\s*Details\s*:\s*/i, "") };
}

export function upstreamSummary(input: { message: string; debug?: DebugSnapshot | null; fallback: string }): string {
  const { message, debug } = input;
  const fromMessage = /HTTP\s+(\d{3})/i.exec(message)?.[1];
  const status = debug && debug.status > 0 ? debug.status : fromMessage ? Number(fromMessage) : 0;
  const streamed2xx = !!debug && debug.status >= 200 && debug.status < 300 && isStreamedBody(debug.responseBody);

  const { head, details } = splitDetails(message);
  const candidates: string[] = [];
  if (debug && !streamed2xx) candidates.push(reasonFromBody(debug.responseBody));
  else if (debug && debug.status >= 300) candidates.push(reasonFromBody(debug.responseBody));
  candidates.push(reasonFromBody(details));
  const errorLine = message.split(/\r?\n/).find((l) => /^\s*Error\s*:/i.test(l));
  if (errorLine) candidates.push(errorLine.replace(/^\s*Error\s*:\s*/i, "").trim());
  const firstLine = head
    .split(/\r?\n/)
    .map((l) => l.trim())
    .find((l) => l && !/^details\s*:/i.test(l) && !/^data:/i.test(l));
  if (firstLine) candidates.push(firstLine.replace(/^HTTP\s+\d{3}\s*[:\-]?\s*/i, ""));
  const reason = candidates.find((c) => c && !/^data:/i.test(c)) ?? input.fallback;
  return status > 0 ? `HTTP ${status}: ${reason}` : reason;
}
