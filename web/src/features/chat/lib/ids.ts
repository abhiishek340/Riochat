/** Identifiers minted on the client. */

const RUN_PREFIX = "run_";
const LOCAL_PREFIX = "local-";

function randomHex(bytes: number): string {
  const buf = new Uint8Array(bytes);
  if (typeof crypto !== "undefined" && typeof crypto.getRandomValues === "function") {
    crypto.getRandomValues(buf);
  } else {
    for (let i = 0; i < bytes; i++) buf[i] = Math.floor(Math.random() * 256);
  }
  let out = "";
  for (const b of buf) out += b.toString(16).padStart(2, "0");
  return out;
}

/** `run_` + 32 hex characters, no hyphens (at most 64 characters overall). */
export function newRunId(): string {
  return RUN_PREFIX + randomHex(16);
}

export function isRunId(value: string): boolean {
  return /^run_[0-9a-f]{32}$/.test(value);
}

/** Local ids can never collide with 32-hex server ids and are never sent to the server. */
export function localUserId(runId: string): string {
  return `${LOCAL_PREFIX}u-${runId}`;
}

export function localAssistantId(runId: string): string {
  return `${LOCAL_PREFIX}a-${runId}`;
}

export function isLocalId(id: string | null | undefined): boolean {
  return typeof id === "string" && id.startsWith(LOCAL_PREFIX);
}

/** A uuid for temporary-chat sessions. */
export function newSessionId(): string {
  if (typeof crypto !== "undefined" && typeof crypto.randomUUID === "function") return crypto.randomUUID();
  const h = randomHex(16);
  return `${h.slice(0, 8)}-${h.slice(8, 12)}-4${h.slice(13, 16)}-a${h.slice(17, 20)}-${h.slice(20, 32)}`;
}

export function isPublicId(value: string | null | undefined): value is string {
  return typeof value === "string" && /^[0-9a-f]{32}$/.test(value);
}
