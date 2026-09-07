import { describe, expect, it } from "vitest";
import { apiUrl } from "@/lib/api/base-url";
import { nativeChanges, nativeFull, nanoText, parseUsdPrice, usdToNano } from "./native";
import { formatProviders, parseProviders, P, stripeWebhookUrl, toggleProvider, validatePayments } from "./payments";
import { blankCodeForm, createBody, editBody, editFormOf, exportRecord, unavailableReason, validateCreate, validateEditChange, type CodeForm, type RedemptionCode } from "./redemption";

const NOW = Date.parse("2026-06-15T12:00:00Z");
const future = "2026-12-31T10:00";
const base: CodeForm = { ...blankCodeForm("usage"), creditUSD: "5" };

function code(over: Partial<RedemptionCode> = {}): RedemptionCode {
  return {
    id: 1, codeHint: "AB…", mode: "usage", rewardType: "credit", creditUSD: 5, creditNanousd: 5e9, planID: 0, durationDays: 0, maxRedemptions: 3, perUserLimit: 1, redeemedCount: 0, remainingRedemptions: 3,
    status: "active", expiresAt: null, description: "", createdByUserID: 1, createdAt: "", updatedAt: "", ...over,
  };
}

describe("code creation", () => {
  it("accepts a plain usage code", () => {
    expect(validateCreate(base, NOW)).toBeNull();
  });
  it("checks the custom code pattern and quantity", () => {
    expect(validateCreate({ ...base, code: "ab" }, NOW)?.key).toBe("adminBilling.codes.problems.customCode");
    expect(validateCreate({ ...base, code: "good_code-1" }, NOW)).toBeNull();
    expect(validateCreate({ ...base, code: "good", quantity: "5" }, NOW)?.key).toBe("adminBilling.codes.problems.customQuantity");
    expect(validateCreate({ ...base, quantity: "0" }, NOW)?.key).toBe("adminBilling.codes.problems.quantity");
  });
  it("checks the limits", () => {
    expect(validateCreate({ ...base, maxRedemptions: "3", perUserLimit: "5" }, NOW)?.key).toBe("adminBilling.codes.problems.perUserOverMax");
    expect(validateCreate({ ...base, maxRedemptions: "abc" }, NOW)?.key).toBe("adminBilling.codes.problems.maxRedemptions");
    expect(validateCreate({ ...base, maxRedemptions: "0" }, NOW)?.key).toBe("adminBilling.codes.problems.maxRedemptions");
    expect(validateCreate({ ...base, perUserLimit: "0" }, NOW)?.key).toBe("adminBilling.codes.problems.perUser");
    expect(validateCreate({ ...base, maxRedemptions: "", perUserLimit: "9" }, NOW)).toBeNull();
  });
  it("needs a future expiry when one is given", () => {
    expect(validateCreate({ ...base, expiresAt: "2020-01-01T00:00" }, NOW)?.key).toBe("adminBilling.codes.problems.expiryPast");
    expect(validateCreate({ ...base, expiresAt: "garbage" }, NOW)?.key).toBe("adminBilling.codes.problems.expiryInvalid");
    expect(validateCreate({ ...base, expiresAt: future }, NOW)).toBeNull();
  });
  it("needs credit for usage codes and a plan with days for period codes", () => {
    expect(validateCreate({ ...base, creditUSD: "0" }, NOW)?.key).toBe("adminBilling.codes.problems.credit");
    const period: CodeForm = { ...blankCodeForm("period"), planId: "", durationDays: "30" };
    expect(validateCreate(period, NOW)?.key).toBe("adminBilling.codes.problems.plan");
    expect(validateCreate({ ...period, planId: "2", durationDays: "0" }, NOW)?.key).toBe("adminBilling.codes.problems.days");
    expect(validateCreate({ ...period, planId: "2" }, NOW)).toBeNull();
  });
  it("builds the request body", () => {
    expect(createBody({ ...base, code: " X1y2 ", maxRedemptions: "4", description: " promo ", expiresAt: future })).toMatchObject({ code: "X1y2", quantity: 1, mode: "usage", creditUSD: 5, maxRedemptions: 4, perUserLimit: 1, description: "promo" });
    const period = createBody({ ...blankCodeForm("period"), planId: "3", durationDays: "7" });
    expect(period).toMatchObject({ mode: "period", planID: 3, durationDays: 7, expiresAt: null });
    expect(period.creditUSD).toBeUndefined();
  });
});

describe("code availability", () => {
  it("applies the reasons in order", () => {
    expect(unavailableReason(code({ status: "inactive", expiresAt: "2020-01-01T00:00:00Z" }), "usage", NOW)).toBe("inactive");
    expect(unavailableReason(code({ expiresAt: "2020-01-01T00:00:00Z", remainingRedemptions: 0 }), "usage", NOW)).toBe("expired");
    expect(unavailableReason(code({ remainingRedemptions: 0 }), "usage", NOW)).toBe("exhausted");
    expect(unavailableReason(code(), "self", NOW)).toBe("selfMode");
  });
  it("accepts both kinds in period mode but only usage codes in usage mode", () => {
    expect(unavailableReason(code({ mode: "usage" }), "period", NOW)).toBeNull();
    expect(unavailableReason(code({ mode: "period" }), "period", NOW)).toBeNull();
    expect(unavailableReason(code({ mode: "period" }), "usage", NOW)).toBe("modeMismatch");
    expect(unavailableReason(code({ mode: "usage" }), "usage", NOW)).toBeNull();
  });
  it("treats unlimited codes as never exhausted", () => {
    expect(unavailableReason(code({ remainingRedemptions: null, maxRedemptions: null }), "usage", NOW)).toBeNull();
  });
});

describe("code edits", () => {
  it("sends only what changed", () => {
    const c = code();
    const form = editFormOf(c);
    expect(editBody(c, form)).toEqual({});
    expect(editBody(c, { ...form, status: "inactive", maxRedemptions: "" })).toEqual({ status: "inactive", maxRedemptions: null });
    expect(editBody(c, { ...form, description: " note " })).toEqual({ description: "note" });
  });
  it("does not complain about an unchanged, already-passed expiry", () => {
    const c = code({ expiresAt: "2020-01-01T00:00:00Z" });
    expect(validateEditChange(c, editFormOf(c), NOW)).toBeNull();
    expect(validateEditChange(c, { ...editFormOf(c), expiresAt: "2021-01-01T00:00" }, NOW)?.key).toBe("adminBilling.codes.problems.expiryPast");
  });
  it("exports every field with the plain code", () => {
    expect(exportRecord(code(), "SECRET")).toMatchObject({ id: 1, code: "SECRET", codeHint: "AB…", mode: "usage", creditUSD: 5 });
  });
});

describe("payments", () => {
  it("parses and formats providers", () => {
    expect(parseProviders("disabled")).toEqual([]);
    expect(parseProviders("Stripe, epay, stripe, paypal")).toEqual(["stripe", "epay"]);
    expect(formatProviders([])).toBe("disabled");
    expect(formatProviders(["epay", "stripe"])).toBe("stripe,epay");
    expect(toggleProvider("stripe", "epay", true)).toBe("stripe,epay");
    expect(toggleProvider("stripe", "stripe", false)).toBe("disabled");
  });
  it("needs the Stripe secrets, stored or typed", () => {
    const draft = { [P.providers]: "stripe", [P.stripeSecret]: "", [P.stripeWebhook]: "" };
    expect(validatePayments(draft, new Set())?.key).toBe("adminBilling.payments.problems.stripeSecret");
    expect(validatePayments(draft, new Set([P.stripeSecret]))?.key).toBe("adminBilling.payments.problems.stripeWebhook");
    expect(validatePayments(draft, new Set([P.stripeSecret, P.stripeWebhook]))).toBeNull();
  });
  it("needs the EPay gateway, types, id and key", () => {
    const draft = { [P.providers]: "epay", [P.epayUrl]: "https://pay.test", [P.epayTypes]: "[]", [P.epayPid]: "1", [P.epayKey]: "" };
    expect(validatePayments(draft, new Set())?.key).toBe("adminBilling.payments.problems.epayTypes");
    const good = { ...draft, [P.epayTypes]: '[{"name":"A","type":"alipay"}]' };
    expect(validatePayments(good, new Set())?.key).toBe("adminBilling.payments.problems.epayKey");
    expect(validatePayments(good, new Set([P.epayKey]))).toBeNull();
    expect(validatePayments({ ...good, [P.epayTypes]: '[{"name":"","type":"x"}]' }, new Set([P.epayKey]))?.key).toBe("adminBilling.payments.problems.epayTypes");
  });
  it("asks for nothing while no provider is on", () => {
    expect(validatePayments({ [P.providers]: "disabled" }, new Set())).toBeNull();
  });
  it("shows the webhook address on the API origin", () => {
    expect(stripeWebhookUrl("https://app.test")).toBe(`${apiUrl("/billing/payments/stripe/webhook").startsWith("http") ? "" : "https://app.test"}${apiUrl("/billing/payments/stripe/webhook")}`);
  });
});

describe("tool prices", () => {
  it("converts USD to nano-USD", () => {
    expect(usdToNano(0.003)).toBe(3_000_000);
    expect(parseUsdPrice("0.0025")).toEqual({ ok: true, nano: 2_500_000 });
    expect(parseUsdPrice("")).toEqual({ ok: true, nano: 0 });
    expect(parseUsdPrice("-1")).toEqual({ ok: false });
    expect(parseUsdPrice("1e3")).toEqual({ ok: false });
    expect(nanoText(2_500_000)).toBe("0.0025");
    expect(nanoText(0)).toBe("0");
  });
  const tools = [
    { provider: "p", toolKey: "search", label: "Search", description: "", type: "", priceNanousd: 1_000_000, unit: "call", priceLabel: "", billable: true },
    { provider: "p", toolKey: "code", label: "Code", description: "", type: "", priceNanousd: 0, unit: "call", priceLabel: "", billable: false },
  ];
  it("saves a changed price as billable when positive", () => {
    expect(nativeChanges(tools, { search: "0.002", code: "0.01" })).toEqual([
      { toolKey: "search", priceNanousd: 2_000_000, unit: "call", priceLabel: "", billable: true },
      { toolKey: "code", priceNanousd: 10_000_000, unit: "call", priceLabel: "", billable: true },
    ]);
    expect(nativeChanges(tools, { search: "0" })[0]).toMatchObject({ billable: false });
  });
  it("keeps the stored price while the typing is invalid", () => {
    expect(nativeChanges(tools, { search: "abc" })).toEqual([]);
    expect(nativeFull(tools, { search: "abc" })[0]?.priceNanousd).toBe(1_000_000);
  });
});
