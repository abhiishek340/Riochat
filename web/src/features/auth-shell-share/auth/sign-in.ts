/**
 * Turns a login-shaped response into one of three outcomes and, on success,
 * records the session. Pure of React so every sign-in path (password, second
 * factor, registration, provider exchange) shares the same rules.
 */
import { adoptSession } from "@/lib/api/endpoints/auth";
import type { AuthPayload } from "@/lib/api/types";
import { isApiError } from "@/lib/api/errors";
import { isDesktop } from "@/lib/api/platform";
import { shell } from "../desktop/bridge";
import { normalizeMethods, type TwoFactorMethod } from "../lib/handoff-storage";

export type SettledAuth =
  | { kind: "challenge"; token: string; methods: TwoFactorMethod[] }
  | { kind: "signed-in" }
  | { kind: "empty" };

/** Desktop only: the rotating refresh token goes to the OS credential store, never to page storage. */
async function rememberRefreshToken(token: string | undefined): Promise<void> {
  if (!token || !isDesktop()) return;
  try {
    await shell.storeSession(token);
  } catch {
    /* the in-memory session still works; the next launch simply asks to sign in again */
  }
}

export async function settleAuth(payload: AuthPayload): Promise<SettledAuth> {
  if (payload.twoFactorRequired) {
    const token = payload.twoFactorChallengeToken ?? "";
    if (!token) return { kind: "empty" };
    return { kind: "challenge", token, methods: normalizeMethods(payload.verificationMethods) };
  }
  if (!payload.accessToken) return { kind: "empty" };
  adoptSession(payload);
  await rememberRefreshToken(payload.refreshToken);
  return { kind: "signed-in" };
}

/** The server answers 401 with this text (and code) once the 5-minute challenge is gone. */
export function isChallengeExpired(error: unknown): boolean {
  if (!isApiError(error) || error.status !== 401) return false;
  return error.rawMessage.trim().toLowerCase() === "two factor challenge expired" || error.errorCode === "auth.two_factor_expired";
}

export function lockMinutes(error: unknown): number | null {
  if (!isApiError(error) || error.errorCode !== "auth.account_locked") return null;
  const seconds = error.retryAfterSeconds;
  return typeof seconds === "number" && seconds > 0 ? Math.ceil(seconds / 60) : null;
}
