import { api, type CallOptions } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import type { AuthUser, LoginOptions } from "@/lib/api/types";
import { fetchLoginOptions } from "@/lib/api/endpoints/auth";
import { bool, isRecord, num, str } from "@/lib/utils/guards";
import type { VerificationMethod } from "../lib/policy";

export interface SendResult {
  sent: boolean;
  debugCode: string;
}

function sendResult(raw: unknown): SendResult {
  const r = isRecord(raw) ? raw : {};
  return { sent: r.sent === true, debugCode: str(r.debugCode) };
}

// ---- reads ---------------------------------------------------------------------

export interface SessionRow {
  sessionID: string;
  current: boolean;
  deviceLabel: string;
  deviceName: string;
  browserName: string;
  osName: string;
  clientIP: string;
  countryCode: string;
  regionName: string;
  cityName: string;
  createdAt: string;
  updatedAt: string;
}

export async function fetchSessions(signal?: AbortSignal): Promise<SessionRow[]> {
  const data = await api.get<unknown>("/auth/sessions", { signal });
  const list = isRecord(data) && Array.isArray(data.results) ? data.results : [];
  return list.flatMap((s): SessionRow[] =>
    isRecord(s)
      ? [
          {
            sessionID: str(s.sessionID),
            current: s.current === true,
            deviceLabel: str(s.deviceLabel),
            deviceName: str(s.deviceName),
            browserName: str(s.browserName),
            osName: str(s.osName),
            clientIP: str(s.clientIP),
            countryCode: str(s.countryCode),
            regionName: str(s.regionName),
            cityName: str(s.cityName),
            createdAt: str(s.createdAt),
            updatedAt: str(s.updatedAt),
          },
        ]
      : [],
  );
}

export interface IdentityRow {
  id: number;
  providerSlug: string;
  providerName: string;
  providerType: string;
  providerLogoURL: string;
  providerDisplayName: string;
  email: string;
  linkedAt: string;
}

export async function fetchIdentities(signal?: AbortSignal): Promise<IdentityRow[]> {
  const data = await api.get<unknown>("/me/identities", { signal });
  const list = isRecord(data) && Array.isArray(data.results) ? data.results : [];
  return list.flatMap((s): IdentityRow[] =>
    isRecord(s)
      ? [
          {
            id: num(s.id),
            providerSlug: str(s.providerSlug),
            providerName: str(s.providerName),
            providerType: str(s.providerType),
            providerLogoURL: str(s.providerLogoURL),
            providerDisplayName: str(s.providerDisplayName),
            email: str(s.email),
            linkedAt: str(s.linkedAt),
          },
        ]
      : [],
  );
}

export interface MfaStatus {
  available: boolean;
  totpEnabled: boolean;
  required: boolean;
  recoveryCount: number;
}

export async function fetchMfaStatus(signal?: AbortSignal): Promise<MfaStatus> {
  const r = await api.get<unknown>("/me/2fa", { signal });
  const o = isRecord(r) ? r : {};
  return { available: bool(o.available, false), totpEnabled: bool(o.totpEnabled, false), required: bool(o.required, false), recoveryCount: num(o.recoveryCount) };
}

export async function fetchCurrentUser(signal?: AbortSignal): Promise<AuthUser> {
  return (await api.get<{ user: AuthUser }>("/me", { signal })).user;
}

export { fetchLoginOptions };
export type { LoginOptions };

// ---- sessions ------------------------------------------------------------------

export async function logoutAll(): Promise<void> {
  await api.post("/auth/logout-all");
}

export async function revokeSession(id: string): Promise<void> {
  await api.post(`/auth/sessions/${seg(id)}/logout`);
}

export async function unlinkIdentity(id: number): Promise<void> {
  await api.delete(`/me/identities/${seg(id)}`);
}

// ---- password ------------------------------------------------------------------

export async function passwordChangeStart(method: VerificationMethod): Promise<SendResult> {
  return sendResult(await api.post<unknown>("/auth/password/change/start", { verificationMethod: method }));
}

export async function passwordChangeComplete(body: {
  currentPassword?: string;
  newPassword: string;
  verificationMethod: VerificationMethod;
  code: string;
}): Promise<void> {
  await api.post("/auth/password/change/complete", body);
}

// ---- email ---------------------------------------------------------------------

export async function emailVerifyCurrentStart(): Promise<SendResult> {
  return sendResult(await api.post<unknown>("/me/email/verify-current/start"));
}
export async function emailVerifyCurrentComplete(code: string): Promise<AuthUser> {
  return (await api.post<{ user: AuthUser }>("/me/email/verify-current/complete", { code })).user;
}
export async function emailBootstrapStart(email: string): Promise<SendResult> {
  return sendResult(await api.post<unknown>("/me/email/bootstrap/start", { email }));
}
export async function emailBootstrapComplete(email: string, code: string): Promise<AuthUser> {
  return (await api.post<{ user: AuthUser }>("/me/email/bootstrap/complete", { email, code })).user;
}
export async function emailChangeStartCurrent(method?: VerificationMethod): Promise<SendResult> {
  return sendResult(await api.post<unknown>("/me/email/change/start-current", method ? { verificationMethod: method } : undefined));
}
export async function emailChangeStartNew(email: string): Promise<SendResult> {
  return sendResult(await api.post<unknown>("/me/email/change/start-new", { email }));
}
export async function emailChangeComplete(body: {
  email: string;
  currentVerificationMethod: VerificationMethod;
  currentCode: string;
  newCode: string;
}): Promise<AuthUser> {
  return (await api.post<{ user: AuthUser }>("/me/email/change/complete", body)).user;
}

// ---- two-factor ------------------------------------------------------------------

export interface TotpSetup {
  secret: string;
  otpauthURL: string;
  expiresAt: string;
}

export async function mfaSetupStart(): Promise<TotpSetup> {
  const r = await api.post<unknown>("/me/2fa/setup/start");
  const o = isRecord(r) ? r : {};
  return { secret: str(o.secret), otpauthURL: str(o.otpauthURL), expiresAt: str(o.expiresAt) };
}
export async function mfaSetupConfirm(code: string): Promise<string[]> {
  const r = await api.post<unknown>("/me/2fa/setup/confirm", { code });
  return isRecord(r) && Array.isArray(r.recoveryCodes) ? r.recoveryCodes.filter((c): c is string => typeof c === "string") : [];
}
export async function mfaSetupCancel(): Promise<void> {
  await api.delete("/me/2fa/setup");
}
export async function mfaRegenerate(code: string): Promise<string[]> {
  const r = await api.post<unknown>("/me/2fa/recovery/regenerate", { code });
  return isRecord(r) && Array.isArray(r.recoveryCodes) ? r.recoveryCodes.filter((c): c is string => typeof c === "string") : [];
}
export async function mfaDisable(code: string): Promise<void> {
  await api.post("/me/2fa/disable", { code });
}

// ---- deletion ----------------------------------------------------------------------

export async function deleteStart(method: VerificationMethod): Promise<SendResult> {
  return sendResult(await api.post<unknown>("/me/delete/start", { verificationMethod: method }));
}
export async function deleteAccount(method: VerificationMethod, code: string): Promise<void> {
  // DELETE carries a JSON body here; the client forwards it although CallOptions does not list it.
  await api.delete("/me", { body: { verificationMethod: method, code } } as CallOptions);
}
