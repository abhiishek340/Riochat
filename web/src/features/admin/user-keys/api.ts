import { api } from "@/lib/api/client";
import { readPage, type PageResult } from "../lib/paging";

export interface PersonalProvider {
  id: string;
  name: string;
  baseURL: string;
  host: string;
  protocol: string;
  icon: string;
  source: "manual" | "link" | (string & {});
  status: "active" | "disabled" | "suspended" | (string & {});
  keyHint: string;
  modelCount: number;
  lastCheckedAt: string | null;
  lastError: string;
  createdAt: string;
  ownerUserID: number;
  ownerPublicID: string;
  ownerUsername: string;
  ownerDisplayName: string;
  ownerLabel: string;
  ownerEmail: string;
  ownerAvatarURL: string;
}

export interface ProviderQuery {
  page: number;
  pageSize: number;
  q: string;
  status: string;
  ownerUserId?: number;
}

export async function listProviders(query: ProviderQuery, signal?: AbortSignal): Promise<PageResult<PersonalProvider>> {
  const raw = await api.get<unknown>("/admin/model-providers", {
    signal,
    query: { page: query.page, page_size: query.pageSize, q: query.q.trim(), status: query.status, owner_user_id: query.ownerUserId },
  });
  return readPage<PersonalProvider>(raw);
}

export async function suspendProviders(ids: string[], suspended: boolean): Promise<number> {
  const out = await api.post<{ affected?: number }>("/admin/model-providers/suspend", { ids, suspended });
  return out?.affected ?? 0;
}

export async function suspendHost(host: string): Promise<number> {
  const out = await api.post<{ affected?: number }>("/admin/model-providers/suspend-host", { host });
  return out?.affected ?? 0;
}

export async function deleteProviders(ids: string[]): Promise<number> {
  const out = await api.post<{ affected?: number }>("/admin/model-providers/batch-delete", { ids });
  return out?.affected ?? 0;
}
