import { api } from "@/lib/api/client";
import type { AuthUser } from "@/lib/api/types";
import { isRecord, num, str } from "@/lib/utils/guards";

async function userFrom(promise: Promise<unknown>): Promise<AuthUser> {
  const data = await promise;
  const user = isRecord(data) && isRecord(data.user) ? data.user : null;
  if (!user) throw new Error("errors.generic");
  return user as AuthUser;
}

export function patchProfile(body: Record<string, unknown>): Promise<AuthUser> {
  return userFrom(api.patch<unknown>("/me", body));
}

export function patchUsername(username: string): Promise<AuthUser> {
  return userFrom(api.patch<unknown>("/me/username", { username }));
}

export function completeOnboarding(newPassword?: string): Promise<AuthUser> {
  return userFrom(api.post<unknown>("/me/onboarding/complete", newPassword ? { newPassword } : undefined));
}

export interface TwoFactorSetup {
  secret: string;
  otpauthURL: string;
  expiresAt: string;
}

export async function startTwoFactorSetup(): Promise<TwoFactorSetup> {
  const data = await api.post<unknown>("/me/2fa/setup/start");
  const d = isRecord(data) ? data : {};
  return { secret: str(d.secret), otpauthURL: str(d.otpauthURL), expiresAt: str(d.expiresAt) };
}

export interface TwoFactorConfirmed {
  recoveryCodes: string[];
  enabled: boolean;
  recoveryCount: number;
}

export async function confirmTwoFactorSetup(code: string): Promise<TwoFactorConfirmed> {
  const data = await api.post<unknown>("/me/2fa/setup/confirm", { code });
  const d = isRecord(data) ? data : {};
  const status = isRecord(d.status) ? d.status : {};
  return {
    recoveryCodes: Array.isArray(d.recoveryCodes) ? d.recoveryCodes.filter((c): c is string => typeof c === "string") : [],
    enabled: status.totpEnabled === true,
    recoveryCount: num(status.recoveryCount),
  };
}

export async function cancelTwoFactorSetup(): Promise<void> {
  await api.delete("/me/2fa/setup");
}
