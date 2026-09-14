import { describe, expect, it } from "vitest";
import { normalizeConfig, normalizeOverview } from "./types";

describe("normalizeConfig", () => {
  it("reads the wrapped document and keeps only known providers", () => {
    const c = normalizeConfig({ config: { mode: "usage", paymentProviders: ["stripe", "bogus", "epay", "stripe"], displayCurrency: "CNY", usdToCNYRate: 7.1, epayTypes: [{ name: "Ali", type: "alipay" }, { type: "" }] } });
    expect(c).toEqual({ mode: "usage", paymentProviders: ["stripe", "epay"], displayCurrency: "CNY", usdToCNYRate: 7.1, epayTypes: [{ name: "Ali", type: "alipay" }] });
  });
  it("reads a bare document and defaults to self / USD", () => {
    expect(normalizeConfig({ mode: "weird", displayCurrency: "EUR" })).toMatchObject({ mode: "self", displayCurrency: "USD", paymentProviders: [] });
    expect(normalizeConfig(null).mode).toBe("self");
  });
});

describe("normalizeOverview", () => {
  it("unwraps the overview and reads the balance from the account", () => {
    const o = normalizeOverview({ overview: { mode: "usage", account: { balanceUSD: 4.5 }, totalSpentUSD: 1.25 } });
    expect(o).toMatchObject({ mode: "usage", balanceUSD: 4.5, totalSpentUSD: 1.25, plan: null, entitlements: [] });
  });
  it("accepts the redemption response, whose overview is nested the same way", () => {
    const o = normalizeOverview({ redemption: {}, overview: { mode: "period", periodCreditUSD: 5, subscriptionEntitlements: [{ id: 2, planID: 3, plan: { id: 3, code: "pro" } }] } });
    expect(o.periodCreditUSD).toBe(5);
    expect(o.entitlements[0]).toMatchObject({ id: 2, planId: 3, plan: { code: "pro" } });
  });
});
