/** "Quick fill" for extra request headers kept as JSON object text. */
import { isPlainObject, prettyJson } from "./json";

export type HeaderFill =
  | { kind: "ok"; text: string; added: string[] }
  | { kind: "unchanged" }
  | { kind: "invalid" }
  | { kind: "conflict"; names: string[] };

/**
 * Adds `entries` to the JSON header text. Names compare case-insensitively.
 * An existing name with the same value is left alone. An existing name with a
 * different value is kept in `skip` mode and refused as a conflict in `strict`
 * mode (nothing is changed then).
 */
export function fillHeaders(text: string, entries: Readonly<Record<string, string>>, mode: "skip" | "strict" = "skip"): HeaderFill {
  const source = text.trim();
  let current: Record<string, unknown> = {};
  if (source !== "") {
    let parsed: unknown;
    try {
      parsed = JSON.parse(source);
    } catch {
      return { kind: "invalid" };
    }
    if (!isPlainObject(parsed)) return { kind: "invalid" };
    current = parsed;
  }
  const lower = new Map(Object.keys(current).map((k) => [k.toLowerCase(), k]));
  const conflicts: string[] = [];
  const toAdd: Array<[string, string]> = [];
  for (const [name, value] of Object.entries(entries)) {
    const existing = lower.get(name.toLowerCase());
    if (existing === undefined) toAdd.push([name, value]);
    else if (current[existing] !== value && mode === "strict") conflicts.push(name);
  }
  if (conflicts.length > 0) return { kind: "conflict", names: conflicts };
  if (toAdd.length === 0) return { kind: "unchanged" };
  const next = { ...current };
  for (const [name, value] of toAdd) next[name] = value;
  return { kind: "ok", text: prettyJson(next), added: toAdd.map(([name]) => name) };
}

/** Templates offered by the upstream header menu. */
export const HEADER_TEMPLATES = {
  conversationId: { "X-Conversation-Id": "${RIOCHAT_CONVERSATION_ID}" },
  sessionId: { "X-Session-Id": "${RIOCHAT_SESSION_ID}" },
  requestId: { "X-Request-Id": "${RIOCHAT_REQUEST_ID}" },
  providerRequestId: { "X-Client-Request-Id": "${RIOCHAT_UPSTREAM_REQUEST_ID}" },
  codexAffinity: {
    "session-id": "${RIOCHAT_SESSION_ID}",
    "thread-id": "${RIOCHAT_CONVERSATION_ID}",
    "x-client-request-id": "${RIOCHAT_CONVERSATION_ID}",
  },
} as const;

/** Template offered by the MCP server dialog. */
export const MCP_USER_CONTEXT_HEADER = { "X-Riochat-User-Context": "${RIOCHAT_SIGNED_USER_CONTEXT}" } as const;
