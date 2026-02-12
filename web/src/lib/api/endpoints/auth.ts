import { api, publicApi } from "../client";
import { sessionStore } from "../session-store";
import type { AuthPayload, AuthUser, LoginOptions } from "../types";
import { isRecord } from "../../utils/guards";

/** Records a completed sign-in in the in-memory session. */
export function adoptSession(payload: Pick<AuthPayload, "accessToken" | "sessionID">): void {
  sessionStore.set({ accessToken: payload.accessToken, sessionID: payload.sessionID });
}

export async function login(input: { username: string; password: string }): Promise<AuthPayload> {
  const payload = await publicApi.post<AuthPayload>("/auth/login", input);
  if (!payload.twoFactorRequired && payload.accessToken) adoptSession(payload);
  return payload;
}

export async function verifyTwoFactor(input: {
  challengeToken: string;
  code: string;
  verificationMethod?: string;
}): Promise<AuthPayload> {
  const payload = await publicApi.post<AuthPayload>("/auth/2fa/verify", input);
  if (payload.accessToken) adoptSession(payload);
  return payload;
}

export function startEmailTwoFactor(challengeToken: string): Promise<unknown> {
  return publicApi.post("/auth/2fa/email/start", { challengeToken });
}

export async function logoutRequest(): Promise<void> {
  try {
    await api.post("/auth/logout", undefined, { refresh: false });
  } catch {
    /* the local session is dropped regardless */
  }
}

export async function fetchMe(signal?: AbortSignal): Promise<AuthUser> {
  const data = await api.get<{ user: AuthUser }>("/me", { signal });
  return data.user;
}

const DEFAULT_BRIDGE = { callbackBaseURL: "", enabled: false, protocolVersion: 0 };

export async function fetchLoginOptions(): Promise<LoginOptions> {
  const data = await publicApi.get<LoginOptions>("/auth/login-options");
  const bridge = isRecord(data.providerAuthBridge) ? data.providerAuthBridge : DEFAULT_BRIDGE;
  return { ...data, providerAuthBridge: { ...DEFAULT_BRIDGE, ...bridge }, providers: data.providers ?? [] };
}
