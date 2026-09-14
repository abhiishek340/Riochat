/** Billing wire shapes, read leniently (older servers omit fields). */
import { isRecord, num, str } from "@/lib/utils/guards";

export type BillingMode = "self" | "period" | "usage";
export type PaymentProvider = "stripe" | "epay";

export interface EpayType {
  name: string;
  type: string;
}

export interface BillingConfig {
  mode: BillingMode;
  paymentProviders: PaymentProvider[];
  displayCurrency: "USD" | "CNY";
  usdToCNYRate: number;
  epayTypes: EpayType[];
}

export interface PlanPrice {
  id: number;
  amountCents: number;
  billingInterval: string;
  currency: string;
  isDefault: boolean;
}

export interface Plan {
  id: number;
  code: string;
  name: string;
  description: string;
  featureJSON: string;
  sortOrder: number;
  periodCreditUSD: number;
  prices: PlanPrice[];
}

export interface Entitlement {
  id: number;
  planId: number;
  startAt: string;
  currentPeriodStartAt: string;
  currentPeriodEndAt: string | null;
  plan: Plan | null;
}

export interface Overview {
  mode: string;
  balanceUSD: number;
  totalSpentUSD: number;
  plan: Plan | null;
  periodStartAt: string;
  periodEndAt: string;
  periodCreditUSD: number;
  periodUsedUSD: number;
  entitlements: Entitlement[];
}

export interface Metrics {
  recordCount: number;
  totalTokens: number;
  callCount: number;
  avgLatencyMS: number;
  billedUSD: number;
}

export interface ModelDay extends Metrics {
  platformModelName: string;
}

export interface DaySummary extends Metrics {
  usageDate: string;
  models: ModelDay[];
}

export interface MonthSummary extends Metrics {
  monthStartAt: string;
}

export interface LedgerItem {
  id: number;
  conversationID: number;
  platformModelName: string;
  isFreeModel: boolean;
  billingAt: string;
  createdAt: string;
  inputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  callCount: number;
  durationSeconds: number;
  latencyMS: number;
  billedUSD: number;
  balanceAfterUSD: number | null;
  pricingSnapshotJSON: string;
}

const MODES: readonly string[] = ["self", "period", "usage"];

export function normalizeConfig(raw: unknown): BillingConfig {
  // The server wraps the document as `{config: {...}}`.
  const root = isRecord(raw) && isRecord(raw.config) ? raw.config : raw;
  const r = isRecord(root) ? root : {};
  const mode = str(r.mode);
  const providers = Array.isArray(r.paymentProviders)
    ? r.paymentProviders.filter((p): p is PaymentProvider => p === "stripe" || p === "epay")
    : [];
  const types = Array.isArray(r.epayTypes)
    ? r.epayTypes.flatMap((e): EpayType[] => {
        if (!isRecord(e)) return [];
        const type = str(e.type).trim();
        return type ? [{ type, name: str(e.name).trim() }] : [];
      })
    : [];
  return {
    mode: (MODES.includes(mode) ? mode : "self") as BillingMode,
    paymentProviders: [...new Set(providers)],
    displayCurrency: r.displayCurrency === "CNY" ? "CNY" : "USD",
    usdToCNYRate: num(r.usdToCNYRate, 0),
    epayTypes: types,
  };
}

export function normalizePrice(raw: unknown): PlanPrice {
  const r = isRecord(raw) ? raw : {};
  return {
    id: num(r.id),
    amountCents: num(r.amountCents),
    billingInterval: str(r.billingInterval),
    currency: str(r.currency, "USD").toUpperCase() || "USD",
    isDefault: r.isDefault === true,
  };
}

export function normalizePlan(raw: unknown): Plan {
  const r = isRecord(raw) ? raw : {};
  return {
    id: num(r.id),
    code: str(r.code),
    name: str(r.name),
    description: str(r.description),
    featureJSON: str(r.featureJSON),
    sortOrder: num(r.sortOrder),
    periodCreditUSD: num(r.periodCreditUSD),
    prices: Array.isArray(r.prices) ? r.prices.map(normalizePrice) : [],
  };
}

export function normalizePlans(raw: unknown): Plan[] {
  return Array.isArray(raw) ? raw.map(normalizePlan) : [];
}

function normalizeEntitlement(raw: unknown): Entitlement {
  const r = isRecord(raw) ? raw : {};
  return {
    id: num(r.id),
    planId: num(r.planID),
    startAt: str(r.startAt),
    currentPeriodStartAt: str(r.currentPeriodStartAt),
    currentPeriodEndAt: typeof r.currentPeriodEndAt === "string" ? r.currentPeriodEndAt : null,
    plan: isRecord(r.plan) ? normalizePlan(r.plan) : null,
  };
}

export function normalizeOverview(raw: unknown): Overview {
  const root = isRecord(raw) && isRecord(raw.overview) ? raw.overview : raw;
  const r = isRecord(root) ? root : {};
  const account = isRecord(r.account) ? r.account : {};
  return {
    mode: str(r.mode, "self"),
    balanceUSD: num(account.balanceUSD),
    totalSpentUSD: num(r.totalSpentUSD),
    plan: isRecord(r.plan) ? normalizePlan(r.plan) : null,
    periodStartAt: str(r.periodStartAt),
    periodEndAt: str(r.periodEndAt),
    periodCreditUSD: num(r.periodCreditUSD),
    periodUsedUSD: num(r.periodUsedUSD),
    entitlements: Array.isArray(r.subscriptionEntitlements) ? r.subscriptionEntitlements.map(normalizeEntitlement) : [],
  };
}

function metrics(r: Record<string, unknown>): Metrics {
  return {
    recordCount: num(r.recordCount),
    totalTokens: num(r.totalTokens),
    callCount: num(r.callCount),
    avgLatencyMS: num(r.avgLatencyMS),
    billedUSD: num(r.billedUSD),
  };
}

export function normalizeDaily(raw: unknown): DaySummary[] {
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((item): DaySummary[] => {
    if (!isRecord(item)) return [];
    const models = Array.isArray(item.models)
      ? item.models.flatMap((m): ModelDay[] => (isRecord(m) ? [{ platformModelName: str(m.platformModelName), ...metrics(m) }] : []))
      : [];
    return [{ usageDate: str(item.usageDate), ...metrics(item), models }];
  });
}

export function normalizeMonthly(raw: unknown): MonthSummary[] {
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((item): MonthSummary[] => (isRecord(item) ? [{ monthStartAt: str(item.monthStartAt), ...metrics(item) }] : []));
}

export function normalizeLedger(raw: unknown): LedgerItem[] {
  if (!Array.isArray(raw)) return [];
  return raw.flatMap((item): LedgerItem[] => {
    if (!isRecord(item)) return [];
    return [
      {
        id: num(item.id),
        conversationID: num(item.conversationID),
        platformModelName: str(item.platformModelName),
        isFreeModel: item.isFreeModel === true,
        billingAt: str(item.billingAt),
        createdAt: str(item.createdAt),
        inputTokens: num(item.inputTokens),
        cacheReadTokens: num(item.cacheReadTokens),
        cacheWriteTokens: num(item.cacheWriteTokens),
        outputTokens: num(item.outputTokens),
        reasoningTokens: num(item.reasoningTokens),
        callCount: num(item.callCount),
        durationSeconds: num(item.durationSeconds),
        latencyMS: num(item.latencyMS),
        billedUSD: num(item.billedUSD),
        balanceAfterUSD: typeof item.balanceAfterUSD === "number" ? item.balanceAfterUSD : null,
        pricingSnapshotJSON: str(item.pricingSnapshotJSON),
      },
    ];
  });
}
