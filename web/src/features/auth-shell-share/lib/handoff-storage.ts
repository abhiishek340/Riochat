/**
 * Short-lived hand-offs kept in sessionStorage between page loads:
 *  - the pending second-factor challenge (survives a refresh of the prompt);
 *  - the identity-provider request (PKCE verifier + state) while the browser
 *    is away at the provider.
 * Every access tolerates unavailable storage.
 */
import { readStorage, removeStorage, writeStorage } from "@/lib/utils/storage";
import { isRecord, oneOf, parseJson, str } from "@/lib/utils/guards";

export const TWO_FACTOR_METHODS = ["two_factor", "email"] as const;
export type TwoFactorMethod = (typeof TWO_FACTOR_METHODS)[number];

export const CHALLENGE_KEY = "riochat:2fa:challenge";
const LEGACY_METHODS_KEY = "riochat:2fa:methods";
/** Matches the server's challenge lifetime. */
export const CHALLENGE_TTL_MS = 5 * 60 * 1000;

export interface PendingChallenge {
  token: string;
  methods: TwoFactorMethod[];
  expiresAt: number;
}

export function normalizeMethods(input: unknown): TwoFactorMethod[] {
  const list = Array.isArray(input) ? input : [];
  const seen: TwoFactorMethod[] = [];
  for (const item of list) {
    if (oneOf(item, TWO_FACTOR_METHODS) && !seen.includes(item)) seen.push(item);
  }
  return seen.length ? seen : ["two_factor"];
}

export function saveChallenge(token: string, methods: unknown, now = Date.now()): boolean {
  if (!token) return false;
  const entry: PendingChallenge = { token, methods: normalizeMethods(methods), expiresAt: now + CHALLENGE_TTL_MS };
  writeStorage(CHALLENGE_KEY, JSON.stringify(entry), "session");
  // writeStorage swallows failures, so read back to learn whether it stuck.
  return readStorage(CHALLENGE_KEY, "session") !== null;
}

/** Reading does not consume the entry; expired, malformed and legacy entries are removed. */
export function readChallenge(now = Date.now()): PendingChallenge | null {
  const raw = readStorage(CHALLENGE_KEY, "session");
  if (raw === null) return null;
  const parsed = parseJson(raw);
  if (!isRecord(parsed)) {
    // A bare string is the old format, together with a separate methods key.
    clearChallenge();
    return null;
  }
  const token = str(parsed.token);
  const expiresAt = typeof parsed.expiresAt === "number" ? parsed.expiresAt : 0;
  if (!token || expiresAt <= now) {
    clearChallenge();
    return null;
  }
  return { token, methods: normalizeMethods(parsed.methods), expiresAt };
}

export function clearChallenge(): void {
  removeStorage(CHALLENGE_KEY, "session");
  removeStorage(LEGACY_METHODS_KEY, "session");
}

export type BridgeIntent = "login" | "register" | "bind";

export interface BridgeRequest {
  verifier: string;
  state: string;
  intent: BridgeIntent;
  next: string;
  expiresAt: number;
}

/** Server transaction window + grant window + clock skew. */
export const BRIDGE_TTL_MS = (10 * 60 + 90 + 30) * 1000;

export function bridgeKey(slug: string): string {
  return `riochat:oauth:${slug}:bridge`;
}

export function saveBridgeRequest(slug: string, request: Omit<BridgeRequest, "expiresAt">, now = Date.now()): void {
  const entry: BridgeRequest = { ...request, expiresAt: now + BRIDGE_TTL_MS };
  writeStorage(bridgeKey(slug), JSON.stringify(entry), "session");
}

export function dropBridgeRequest(slug: string): void {
  removeStorage(bridgeKey(slug), "session");
}

/**
 * Single use: the stored request is removed whatever happens next, so a grant
 * can never be exchanged twice from the same hand-off.
 */
export function takeBridgeRequest(slug: string, now = Date.now()): BridgeRequest | null {
  const raw = readStorage(bridgeKey(slug), "session");
  dropBridgeRequest(slug);
  if (raw === null) return null;
  const parsed = parseJson(raw);
  if (!isRecord(parsed)) return null;
  const verifier = str(parsed.verifier);
  const state = str(parsed.state);
  const expiresAt = typeof parsed.expiresAt === "number" ? parsed.expiresAt : 0;
  const intent = parsed.intent;
  if (!verifier || !state || expiresAt <= now || !oneOf(intent, ["login", "register", "bind"] as const)) return null;
  return { verifier, state, intent, next: str(parsed.next), expiresAt };
}
