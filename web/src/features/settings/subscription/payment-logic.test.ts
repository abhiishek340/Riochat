import { describe, expect, it } from "vitest";
import { checkoutUrls, methodOptions, paymentEnabled, reconcileSelection } from "./payment-logic";
import type { BillingConfig } from "./types";

const config = (over: Partial<BillingConfig> = {}): BillingConfig => ({ mode: "period", paymentProviders: ["stripe", "epay"], displayCurrency: "USD", usdToCNYRate: 0, epayTypes: [], ...over });

describe("methodOptions", () => {
  it("offers Stripe and the default EPay types when none are configured", () => {
    const opts = methodOptions(config());
    expect(opts.map((o) => `${o.provider}:${o.epayType ?? ""}`)).toEqual(["stripe:", "epay:alipay", "epay:wxpay"]);
  });
  it("uses configured EPay types, skipping blank ones, and labels them", () => {
    const opts = methodOptions(config({ paymentProviders: ["epay"], epayTypes: [{ type: "qqpay", name: "" }, { type: " ", name: "x" }, { type: "bank", name: "Bank" }, { type: "weird", name: "" }] }));
    expect(opts.map((o) => o.label)).toEqual([{ known: "qqpay" }, { name: "Bank" }, { custom: "weird" }]);
  });
  it("is empty without a config", () => {
    expect(methodOptions(null)).toEqual([]);
    expect(paymentEnabled(null)).toBe(false);
    expect(paymentEnabled(config({ paymentProviders: [] }))).toBe(false);
    expect(paymentEnabled(config())).toBe(true);
  });
});

describe("reconcileSelection", () => {
  it("falls back to the first configured method when the old one disappears", () => {
    const opts = methodOptions(config({ paymentProviders: ["epay"], epayTypes: [{ type: "wxpay", name: "" }] }));
    expect(reconcileSelection({ provider: "stripe", epayType: "alipay" }, opts)).toEqual({ provider: "epay", epayType: "wxpay" });
  });
  it("falls back to the first EPay type when the chosen one vanished", () => {
    const opts = methodOptions(config({ epayTypes: [{ type: "wxpay", name: "" }] }));
    expect(reconcileSelection({ provider: "epay", epayType: "alipay" }, opts)).toEqual({ provider: "epay", epayType: "wxpay" });
  });
  it("keeps a valid selection", () => {
    const sel = { provider: "epay" as const, epayType: "wxpay" };
    expect(reconcileSelection(sel, methodOptions(config()))).toBe(sel);
  });
});

describe("checkoutUrls", () => {
  it("returns to the subscription page", () => {
    expect(checkoutUrls("https://chat.test/")).toEqual({
      successURL: "https://chat.test/settings/subscription?payment=success",
      cancelURL: "https://chat.test/settings/subscription?payment=cancel",
    });
  });
});
