/** Wire calls for sign-in, registration, reset and the identity-provider bridge. */
import { api, publicApi } from "@/lib/api/client";
import type { AuthPayload } from "@/lib/api/types";
import { isRecord, str } from "@/lib/utils/guards";

export const CLIENT_ID_WEB = "riochat-web";
export const CLIENT_ID_DESKTOP = "com.riochat.desktop";

export function loginWithPassword(username: string, password: string): Promise<AuthPayload> {
  return publicApi.post<AuthPayload>("/auth/login", { username, password });
}

export function verifySecondFactor(input: { challengeToken: string; verificationMethod: string; code: string }): Promise<AuthPayload> {
  return publicApi.post<AuthPayload>("/auth/2fa/verify", input);
}

export interface CodeSent {
  sent: boolean;
  expiresAt: string;
  debugCode: string;
}

function readSent(data: unknown): CodeSent {
  const d = isRecord(data) ? data : {};
  return { sent: d.sent === true, expiresAt: str(d.expiresAt), debugCode: str(d.debugCode) };
}

export async function startEmailSecondFactor(challengeToken: string): Promise<CodeSent> {
  return readSent(await publicApi.post<unknown>("/auth/2fa/email/start", { challengeToken }));
}

export async function startRegistrationCode(email: string, turnstileToken?: string): Promise<CodeSent> {
  const body: Record<string, unknown> = { email };
  if (turnstileToken) body.turnstileToken = turnstileToken;
  return readSent(await publicApi.post<unknown>("/auth/register/email/start", body));
}

export function completeRegistration(input: { email: string; password: string; code: string; turnstileToken?: string }): Promise<AuthPayload> {
  const body: Record<string, unknown> = { email: input.email, password: input.password, code: input.code };
  if (input.turnstileToken) body.turnstileToken = input.turnstileToken;
  return publicApi.post<AuthPayload>("/auth/register/email/complete", body);
}

export async function startPasswordReset(email: string): Promise<CodeSent> {
  return readSent(await publicApi.post<unknown>("/auth/password/reset/start", { email }));
}

export async function completePasswordReset(input: { email: string; code: string; newPassword: string }): Promise<boolean> {
  const data = await publicApi.post<unknown>("/auth/password/reset/complete", input);
  return isRecord(data) ? data.changed !== false : true;
}

export interface AuthorizeInput {
  clientID: string;
  redirectURI: string;
  codeChallenge: string;
  clientState: string;
  intent?: "login" | "register";
  next: string;
}

export async function authorizeProvider(slug: string, input: AuthorizeInput, bind: boolean): Promise<string> {
  const body: Record<string, unknown> = {
    clientID: input.clientID,
    redirectURI: input.redirectURI,
    codeChallenge: input.codeChallenge,
    clientState: input.clientState,
    next: input.next,
  };
  if (!bind) body.intent = input.intent ?? "login";
  const path = bind
    ? `/me/identities/providers/${encodeURIComponent(slug)}/authorize`
    : `/auth/providers/${encodeURIComponent(slug)}/authorize`;
  const data = bind ? await api.post<unknown>(path, body) : await publicApi.post<unknown>(path, body);
  return isRecord(data) ? str(data.authorizationURL) : "";
}

export interface ExchangeInput {
  clientID: string;
  grant: string;
  codeVerifier: string;
}

export function exchangeProviderGrant(slug: string, input: ExchangeInput): Promise<AuthPayload> {
  return publicApi.post<AuthPayload>(`/auth/providers/${encodeURIComponent(slug)}/exchange`, input);
}

export function exchangeBindGrant(slug: string, input: ExchangeInput): Promise<unknown> {
  return api.post<unknown>(`/me/identities/providers/${encodeURIComponent(slug)}/exchange`, input);
}
