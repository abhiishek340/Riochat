import { describe, expect, it } from "vitest";
import { actionDisabled, actionKind, actionVariant, defaultPrice, isCurrentPlan, planFeatures, planRank, protectedRank, resolveCurrentPlan, sortEntitlements, usagePercent } from "./plans-logic";
import type { Entitlement, Overview, Plan } from "./types";

const plan = (over: Partial<Plan> = {}): Plan => ({
  id: 1,
  code: "pro",
  name: "Pro",
  description: "",
  featureJSON: "",
  sortOrder: 0,
  periodCreditUSD: 5,
  prices: [{ id: 11, amountCents: 2000, billingInterval: "month", currency: "USD", isDefault: false }],
  ...over,
});
const free = plan({ id: 2, code: "free", name: "Free", prices: [{ id: 21, amountCents: 0, billingInterval: "month", currency: "USD", isDefault: true }] });
const max = plan({ id: 3, code: "max" });
const overview = (p: Plan | null, entitlements: Entitlement[] = []): Overview => ({
  mode: "period", balanceUSD: 0, totalSpentUSD: 0, plan: p, periodStartAt: "", periodEndAt: "", periodCreditUSD: 0, periodUsedUSD: 0, entitlements,
});
const ent = (p: Plan, id = 1): Entitlement => ({ id, planId: p.id, startAt: "2025-02-01", currentPeriodStartAt: "", currentPeriodEndAt: null, plan: p });

describe("ranks and prices", () => {
  it("ranks by sortOrder, then code, then credit", () => {
    expect(planRank(plan({ sortOrder: 7 }))).toBe(7);
    expect(planRank(plan({ code: "ultra" }))).toBe(40);
    expect(planRank(plan({ code: "max" }))).toBe(30);
    expect(planRank(plan({ code: "pro" }))).toBe(20);
    expect(planRank(free)).toBe(10);
    expect(planRank(plan({ code: "team", periodCreditUSD: 12.5 }))).toBe(12.5);
  });
  it("prefers the default price, else the first", () => {
    const p = plan({ prices: [{ id: 1, amountCents: 1, billingInterval: "month", currency: "USD", isDefault: false }, { id: 2, amountCents: 2, billingInterval: "year", currency: "USD", isDefault: true }] });
    expect(defaultPrice(p)?.id).toBe(2);
    expect(defaultPrice(plan({ prices: p.prices.map((x) => ({ ...x, isDefault: false })) }))?.id).toBe(1);
    expect(defaultPrice(plan({ prices: [] }))).toBeUndefined();
  });
});

describe("current plan", () => {
  it("matches the overview plan, the user's plan id or tier", () => {
    expect(isCurrentPlan(plan(), overview(plan()), {})).toBe(true);
    expect(isCurrentPlan(plan(), null, { planId: 1 })).toBe(true);
    expect(isCurrentPlan(plan(), null, { tier: "pro" })).toBe(true);
    expect(isCurrentPlan(plan(), null, { planId: 9, tier: "max" })).toBe(false);
  });
  it("resolves from the overview first, then the plan list", () => {
    expect(resolveCurrentPlan([free, plan()], overview(max), {})).toBe(max);
    expect(resolveCurrentPlan([free, plan()], null, { tier: "pro" })?.id).toBe(1);
    expect(resolveCurrentPlan([free], null, {})).toBeNull();
  });
});

describe("actionKind", () => {
  const pro = plan();
  const rank = protectedRank(pro, []);
  it("computes the protected rank without free plans", () => {
    expect(rank).toBe(20);
    expect(protectedRank(free, [ent(free)])).toBe(0);
    expect(protectedRank(free, [ent(max)])).toBe(30);
  });
  it("labels the current plan", () => {
    expect(actionKind({ plan: free, isCurrent: true, current: free, protectedRank: 0 })).toBe("current");
    expect(actionKind({ plan: pro, isCurrent: true, current: pro, protectedRank: rank })).toBe("renew");
    expect(actionKind({ plan: plan({ prices: [] }), isCurrent: true, current: pro, protectedRank: rank })).toBe("current");
  });
  it("blocks the free plan while a paid one is protected", () => {
    expect(actionKind({ plan: free, isCurrent: false, current: pro, protectedRank: rank })).toBe("freeBlocked");
  });
  it("handles missing and zero prices", () => {
    expect(actionKind({ plan: plan({ prices: [] }), isCurrent: false, current: free, protectedRank: 0 })).toBe("unavailable");
    expect(actionKind({ plan: free, isCurrent: false, current: null, protectedRank: 0 })).toBe("switch");
  });
  it("subscribes from nothing or from free", () => {
    expect(actionKind({ plan: pro, isCurrent: false, current: null, protectedRank: 0 })).toBe("subscribe");
    expect(actionKind({ plan: pro, isCurrent: false, current: free, protectedRank: 0 })).toBe("subscribe");
  });
  it("upgrades upward and renews downward or sideways", () => {
    expect(actionKind({ plan: max, isCurrent: false, current: pro, protectedRank: 20 })).toBe("upgrade");
    expect(actionKind({ plan: pro, isCurrent: false, current: max, protectedRank: 30 })).toBe("renew");
    expect(actionKind({ plan: plan({ id: 9, code: "pro2", sortOrder: 20 }), isCurrent: false, current: pro, protectedRank: 20 })).toBe("renew");
  });
  it("derives variants and disabling", () => {
    expect(actionVariant("current")).toBe("secondary");
    expect(actionVariant("switch")).toBe("outline");
    expect(actionVariant("upgrade")).toBe("default");
    expect(actionDisabled("freeBlocked")).toBe(true);
    expect(actionDisabled("renew")).toBe(false);
  });
});

describe("planFeatures", () => {
  const fallback = ["credit line", "no free models"];
  it("reads a JSON array of strings", () => {
    expect(planFeatures({ featureJSON: '["a"," b ",3,""]', description: "d" }, fallback)).toEqual(["a", "b"]);
  });
  it("reads an object with a features array", () => {
    expect(planFeatures({ featureJSON: '{"features":["x"]}', description: "d" }, fallback)).toEqual(["x"]);
  });
  it("falls back when nothing usable is listed", () => {
    expect(planFeatures({ featureJSON: "[]", description: "d" }, fallback)).toEqual(fallback);
  });
  it("puts the description first for invalid or absent JSON", () => {
    expect(planFeatures({ featureJSON: "{oops", description: "Great" }, fallback)).toEqual(["Great", ...fallback]);
    expect(planFeatures({ featureJSON: "", description: "" }, fallback)).toEqual(fallback);
  });
});

describe("entitlements and usage", () => {
  it("orders by period start, falling back to start, then id", () => {
    const a = { ...ent(pro(), 2), currentPeriodStartAt: "2025-03-01" };
    const b = { ...ent(pro(), 1), currentPeriodStartAt: "", startAt: "2025-01-01" };
    const c = { ...ent(pro(), 3), currentPeriodStartAt: "2025-03-01" };
    expect(sortEntitlements([a, c, b]).map((e) => e.id)).toEqual([1, 2, 3]);
  });
  it("clamps the percentage and tolerates a zero credit", () => {
    expect(usagePercent(5, 10)).toBe(50);
    expect(usagePercent(30, 10)).toBe(100);
    expect(usagePercent(-1, 10)).toBe(0);
    expect(usagePercent(5, 0)).toBe(0);
  });
});

function pro() {
  return plan();
}
