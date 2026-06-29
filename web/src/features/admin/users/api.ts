import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { readPage, type PageResult } from "../lib/paging";
import type { UserRecord } from "../lib/wire";
import type { CreateBody, EditPatch } from "./rules";

export interface UserQuery {
  page: number;
  pageSize: number;
  q: string;
  subscriptionStatus: string;
  identityProvider: string;
}

export async function listUsers(query: UserQuery, signal?: AbortSignal): Promise<PageResult<UserRecord>> {
  const raw = await api.get<unknown>("/admin/users", {
    signal,
    query: { page: query.page, page_size: query.pageSize, q: query.q.trim(), subscription_status: query.subscriptionStatus, identity_provider: query.identityProvider },
  });
  return readPage<UserRecord>(raw);
}

export async function createUser(body: CreateBody): Promise<UserRecord> {
  const out = await api.post<{ user: UserRecord }>("/admin/users", body);
  return out.user;
}

export async function patchUser(id: number, body: EditPatch): Promise<UserRecord> {
  const out = await api.patch<{ user: UserRecord }>(`/admin/users/${seg(id)}`, body);
  return out.user;
}

export async function deleteUser(id: number): Promise<void> {
  await api.delete(`/admin/users/${seg(id)}`);
}

export async function resetPassword(id: number, newPassword: string): Promise<void> {
  await api.post(`/admin/users/${seg(id)}/reset-password`, { newPassword, mustResetPassword: true });
}

export async function resetTwoFactor(id: number): Promise<void> {
  await api.post(`/admin/users/${seg(id)}/reset-2fa`);
}

export async function revokeSessions(id: number): Promise<void> {
  await api.post(`/admin/users/${seg(id)}/revoke-sessions`);
}

export interface BalanceAccount {
  userID: number;
  balanceUSD: number;
  balanceNanousd: number;
  currency: string;
  status: string;
}

export async function setBalance(id: number, balanceUSD: number, description: string): Promise<BalanceAccount> {
  const out = await api.patch<{ account: BalanceAccount }>(`/admin/billing/accounts/${seg(id)}/balance`, { balanceUSD, description });
  return out.account;
}

export interface ImportReport {
  source: string;
  dedupeField: string;
  dedupeRule: string;
  scanned: number;
  imported: number;
  skippedExistingEmail: number;
  skippedDuplicateSourceEmail: number;
  skippedInvalidEmail: number;
  skippedInvalidRow: number;
}

export function importOpenWebUi(body: { dsn: string; creditMultiplier: number; dryRun: boolean }): Promise<ImportReport> {
  return api.post<ImportReport>("/admin/users/import/openwebui", body);
}
