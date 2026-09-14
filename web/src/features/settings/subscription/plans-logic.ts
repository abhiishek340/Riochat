/** Which plan is current, what each plan's button does, and what it lists. */
import type { Entitlement, Overview, Plan, PlanPrice } from "./types";

export type ActionKind = "current" | "renew" | "freeBlocked" | "unavailable" | "switch" | "subscribe" | "upgrade";

const CODE_RANK: Record<string, number> = { ultra: 40, max: 30, pro: 20, free: 10 };

export function isFreePlan(plan: Pick<Plan, "code">): boolean {
  return plan.code.toLowerCase() === "free";
}

/** The price flagged default, else the first one. */
export function defaultPrice(plan: Plan): PlanPrice | undefined {
  return plan.prices.find((p) => p.isDefault) ?? plan.prices[0];
}

export interface UserPlanRef {
  planId?: number;
  tier?: string;
}

export function isCurrentPlan(plan: Plan, overview: Overview | null, user: UserPlanRef): boolean {
  if (overview?.plan && overview.plan.id === plan.id) return true;
  if (user.planId !== undefined && user.planId > 0 && user.planId === plan.id) return true;
  return Boolean(user.tier) && user.tier === plan.code;
}

/** The overview's plan, else the plan matching the user's id/tier. */
export function resolveCurrentPlan(plans: readonly Plan[], overview: Overview | null, user: UserPlanRef): Plan | null {
  if (overview?.plan) return overview.plan;
  return plans.find((p) => isCurrentPlan(p, null, user)) ?? null;
}

/** sortOrder when positive, else a rank by code, else the period credit. */
export function planRank(plan: Plan): number {
  if (plan.sortOrder > 0) return plan.sortOrder;
  const byCode = CODE_RANK[plan.code.toLowerCase()];
  if (byCode !== undefined) return byCode;
  return plan.periodCreditUSD;
}

/** Highest rank among the current plan and all entitlements, free plans excluded. */
export function protectedRank(current: Plan | null, entitlements: readonly Entitlement[]): number {
  let rank = 0;
  const consider = (p: Plan | null | undefined) => {
    if (p && !isFreePlan(p)) rank = Math.max(rank, planRank(p));
  };
  consider(current);
  for (const e of entitlements) consider(e.plan);
  return rank;
}

export function actionKind(input: {
  plan: Plan;
  isCurrent: boolean;
  current: Plan | null;
  protectedRank: number;
}): ActionKind {
  const { plan, isCurrent, current } = input;
  const price = defaultPrice(plan);
  if (isCurrent) return isFreePlan(plan) ? "current" : price ? "renew" : "current";
  if (isFreePlan(plan) && input.protectedRank > 0) return "freeBlocked";
  if (!price) return "unavailable";
  if (price.amountCents === 0) return "switch";
  if (!current || isFreePlan(current)) return "subscribe";
  if (input.protectedRank > planRank(plan)) return "renew";
  if (planRank(plan) > planRank(current)) return "upgrade";
  return "renew";
}

/** Variant of a plan's button. */
export function actionVariant(kind: ActionKind): "secondary" | "outline" | "default" {
  if (kind === "current") return "secondary";
  if (kind === "freeBlocked" || kind === "unavailable" || kind === "switch") return "outline";
  return "default";
}

export function actionDisabled(kind: ActionKind): boolean {
  return kind === "current" || kind === "freeBlocked" || kind === "unavailable";
}

/**
 * Feature lines of a plan: a JSON array of strings, or an object with a
 * `features` array. Nothing usable falls back; an unparsable or absent JSON
 * puts the description first.
 */
export function planFeatures(plan: Pick<Plan, "featureJSON" | "description">, fallback: readonly string[]): string[] {
  const raw = plan.featureJSON.trim();
  if (raw) {
    try {
      const data: unknown = JSON.parse(raw);
      const list = Array.isArray(data) ? data : data !== null && typeof data === "object" && Array.isArray((data as { features?: unknown }).features) ? (data as { features: unknown[] }).features : null;
      if (list) {
        const lines = list.filter((x): x is string => typeof x === "string" && x.trim() !== "").map((x) => x.trim());
        return lines.length ? lines : [...fallback];
      }
    } catch {
      /* fall through to the description */
    }
  }
  const description = plan.description.trim();
  return description ? [description, ...fallback] : [...fallback];
}

export function sortEntitlements(list: readonly Entitlement[]): Entitlement[] {
  const key = (e: Entitlement) => e.currentPeriodStartAt || e.startAt;
  return [...list].sort((a, b) => key(a).localeCompare(key(b)) || a.id - b.id);
}

/** Percent of the period credit used, clamped to 0..100; 0 without a credit. */
export function usagePercent(used: number, credit: number): number {
  if (!(credit > 0)) return 0;
  return Math.min(100, Math.max(0, (used / credit) * 100));
}
