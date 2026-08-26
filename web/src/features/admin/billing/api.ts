import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { readPage, type PageResult } from "../lib/paging";
import { invalidateReference } from "../lib/reference";
import { readBillingConfig, type BillingConfig, type BillingMode, type BillingPlan, type ModelPrice } from "../lib/wire";
import type { OfficialCatalog } from "./official";
import type { CreateBody, EditBody, RedemptionCode } from "./redemption";
import type { PriceBody } from "./pricing";

export interface ConfigPatch {
  mode: BillingMode;
  prepaidAmountUSD?: number;
  usdToCNYRate?: number;
  displayCurrency?: "USD" | "CNY";
  nativeToolBillingEnabled?: boolean;
  nativeToolPricing?: Array<{ toolKey: string; priceNanousd: number; unit: string; priceLabel: string; billable: boolean }>;
}

/** Any change to the configuration drops the shared reference data. */
export async function patchConfig(patch: ConfigPatch): Promise<BillingConfig> {
  const out = await api.patch<{ config?: unknown }>("/admin/billing/config", patch);
  invalidateReference("billing");
  return readBillingConfig(out?.config);
}

export async function updatePlan(id: number, body: Record<string, unknown>): Promise<BillingPlan> {
  const out = await api.patch<{ plan: BillingPlan }>(`/admin/billing/plans/${seg(id)}`, body);
  invalidateReference("billing");
  return out.plan;
}

export interface CodeQuery {
  page: number;
  pageSize: number;
  q: string;
  mode: string;
  status: string;
  availability: string;
}

export async function listCodes(query: CodeQuery, signal?: AbortSignal): Promise<PageResult<RedemptionCode>> {
  const raw = await api.get<unknown>("/admin/billing/redemption-codes", {
    signal,
    query: { page: query.page, page_size: query.pageSize, q: query.q.trim(), mode: query.mode, status: query.status, availability: query.availability },
  });
  return readPage<RedemptionCode>(raw);
}

export async function createCodes(body: CreateBody): Promise<RedemptionCode[]> {
  const out = await api.post<{ results?: RedemptionCode[] }>("/admin/billing/redemption-codes", body);
  return out?.results ?? [];
}

/** The server wraps the updated code: `{ code: RedemptionCode }`. */
export async function patchCode(id: number, body: EditBody): Promise<RedemptionCode> {
  const out = await api.patch<{ code: RedemptionCode }>(`/admin/billing/redemption-codes/${seg(id)}`, body);
  return out.code;
}

/** The server answers with the full code object; the plaintext is `code.code`. */
export async function revealCode(id: number): Promise<string> {
  const out = await api.get<{ code?: { code?: string } }>(`/admin/billing/redemption-codes/${seg(id)}/code`);
  return out?.code?.code ?? "";
}

export async function deleteCode(id: number): Promise<void> {
  await api.delete(`/admin/billing/redemption-codes/${seg(id)}`);
}

export function batchDeleteCodes(ids: number[]) {
  return api.post<{ total: number; successCount: number; notFoundCount: number; failedCount: number; results: Array<{ id: number; status: string; error?: string }> }>("/admin/billing/redemption-codes/batch-delete", { ids });
}

export async function savePrice(body: PriceBody): Promise<ModelPrice> {
  const out = await api.put<{ modelPricing: ModelPrice }>("/admin/billing/model-prices", body);
  invalidateReference("prices");
  return out.modelPricing;
}

export async function loadOfficial(refresh: boolean, signal?: AbortSignal): Promise<OfficialCatalog> {
  return api.get<OfficialCatalog>("/admin/billing/official-pricing/openrouter", { signal, query: refresh ? { refresh: true } : undefined });
}

export interface CodeRedemption {
  id: number;
  userID: number;
  username: string;
  userDisplayName: string;
  userLabel: string;
  rewardType: string;
  creditUSD: number;
  durationDays: number;
  planName: string;
  balanceBeforeNanousd: number | null;
  balanceAfterNanousd: number | null;
  createdAt: string;
}

export async function listCodeRedemptions(codeId: number, page: number, pageSize: number, signal?: AbortSignal): Promise<PageResult<CodeRedemption>> {
  return readPage<CodeRedemption>(await api.get<unknown>("/admin/redemptions", { signal, query: { code_id: codeId, page, page_size: pageSize } }));
}
