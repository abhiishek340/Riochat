import { api } from "@/lib/api/client";
import { num, str, isRecord } from "@/lib/utils/guards";
import {
  normalizeConfig,
  normalizeDaily,
  normalizeLedger,
  normalizeMonthly,
  normalizeOverview,
  normalizePlans,
  type BillingConfig,
  type DaySummary,
  type LedgerItem,
  type MonthSummary,
  type Overview,
  type Plan,
} from "./types";

export const fetchConfig = async (signal?: AbortSignal): Promise<BillingConfig> => normalizeConfig(await api.get<unknown>("/billing/config", { signal }));
export const fetchPlans = async (signal?: AbortSignal): Promise<Plan[]> => normalizePlans(await api.get<unknown>("/billing/plans", { signal }));
export const fetchOverview = async (signal?: AbortSignal): Promise<Overview> => normalizeOverview(await api.get<unknown>("/billing/overview", { signal }));
export const fetchDaily = async (signal?: AbortSignal): Promise<DaySummary[]> => normalizeDaily(await api.get<unknown>("/billing/usage/daily", { signal }));
export const fetchMonthly = async (signal?: AbortSignal): Promise<MonthSummary[]> =>
  normalizeMonthly(await api.get<unknown>("/billing/usage/monthly", { query: { months: 12 }, signal }));

export interface UsageQuery {
  page: number;
  pageSize: number;
  query: string;
  status: "" | "free" | "billable";
  sort: string;
}

export async function fetchUsage(q: UsageQuery, signal?: AbortSignal): Promise<{ items: LedgerItem[]; total: number }> {
  const data = await api.get<unknown>("/billing/usage", {
    query: { page: q.page, page_size: q.pageSize, query: q.query.trim(), status: q.status, sort: q.sort },
    signal,
  });
  const r = isRecord(data) ? data : {};
  return { items: normalizeLedger(r.results), total: num(r.total) };
}

export interface ActivityDay {
  date: string;
  requestCount: number;
  tokenUsage: number;
}

export async function fetchActivity(signal?: AbortSignal): Promise<ActivityDay[]> {
  const data = await api.get<unknown>("/user/stats/activity", { query: { days: 365 }, signal });
  if (!Array.isArray(data)) return [];
  return data
    .flatMap((d): ActivityDay[] => (isRecord(d) ? [{ date: str(d.date), requestCount: num(d.requestCount), tokenUsage: num(d.tokenUsage) }] : []))
    .sort((a, b) => a.date.localeCompare(b.date));
}

export async function subscribeFree(priceID: number): Promise<void> {
  await api.post("/billing/subscriptions", { priceID, cycles: 1 });
}

export interface CheckoutBody {
  orderType: "subscription" | "topup";
  priceID?: number;
  amountMinorUnits?: number;
  cycles: number;
  paymentProvider: "stripe" | "epay";
  epayType?: string;
  successURL: string;
  cancelURL: string;
}

export async function createCheckout(body: CheckoutBody): Promise<string> {
  const data = await api.post<unknown>("/billing/payments/checkout", body);
  const checkout = isRecord(data) && isRecord(data.checkout) ? data.checkout : {};
  return str(checkout.checkoutURL).trim();
}

export async function redeemCode(code: string): Promise<Overview> {
  return normalizeOverview(await api.post<unknown>("/billing/redemptions", { code }));
}
