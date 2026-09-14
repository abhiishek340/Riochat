import { describe, expect, it } from "vitest";
import { displayOf, formatCost, formatMoney, fromDisplay, isValidTopup, minorUnits, normalizeInterval, parseAmount, payableFor, priceAmountText, toDisplay } from "./money";

const usd = displayOf(null);
const cny = displayOf({ displayCurrency: "CNY", usdToCNYRate: 7 });

describe("display currency", () => {
  it("converts only with CNY and a positive finite rate", () => {
    expect(cny).toEqual({ currency: "CNY", rate: 7 });
    expect(displayOf({ displayCurrency: "CNY", usdToCNYRate: 0 })).toEqual({ currency: "USD", rate: 1 });
    expect(displayOf({ displayCurrency: "CNY", usdToCNYRate: Number.NaN })).toEqual({ currency: "USD", rate: 1 });
    expect(displayOf({ displayCurrency: "USD", usdToCNYRate: 7 })).toEqual({ currency: "USD", rate: 1 });
  });
  it("converts both ways", () => {
    expect(toDisplay(2, cny)).toBe(14);
    expect(fromDisplay(14, cny)).toBe(2);
    expect(toDisplay(2, usd)).toBe(2);
  });
});

describe("formatting", () => {
  it("formats balances with two to four fraction digits", () => {
    expect(formatMoney(12.5, usd)).toBe("$12.50");
    expect(formatMoney(0.12345, usd)).toBe("$0.1235");
    expect(formatMoney(1, cny, "en-US")).toContain("7.00");
  });
  it("formats costs: zero, tiny and ordinary", () => {
    expect(formatCost(0, usd)).toBe("$0.00");
    expect(formatCost(0.00001, usd)).toBe("< $0.0001");
    expect(formatCost(3.2, usd)).toBe("$3.20");
  });
  it("labels plan prices", () => {
    expect(priceAmountText({ amountCents: 1999, currency: "USD" }, usd)).toBe("$19.99");
    expect(priceAmountText({ amountCents: 5000, currency: "EUR" }, usd)).toContain("50.00");
    expect(priceAmountText(undefined, usd)).toBe("-");
    expect(normalizeInterval("lifetime")).toBe("lifetime");
    expect(normalizeInterval("year")).toBe("year");
    expect(normalizeInterval("weekly")).toBe("month");
  });
});

describe("top-up amounts", () => {
  it("rejects zero, negatives and junk", () => {
    expect(isValidTopup(parseAmount("0"))).toBe(false);
    expect(isValidTopup(parseAmount("abc"))).toBe(false);
    expect(isValidTopup(parseAmount(""))).toBe(false);
    expect(isValidTopup(parseAmount("-5"))).toBe(false);
    expect(isValidTopup(parseAmount("0.001"))).toBe(false);
    expect(isValidTopup(parseAmount(" 20 "))).toBe(true);
  });
  it("sends minor units of the display currency", () => {
    expect(minorUnits(20)).toBe(2000);
    expect(minorUnits(0.015)).toBe(2); // rounds
  });
});

describe("payableFor", () => {
  it("charges Stripe in USD unless the display is CNY", () => {
    expect(payableFor("stripe", 10, usd)).toEqual({ currency: "USD", amount: 10 });
    expect(payableFor("stripe", 10, cny)).toEqual({ currency: "CNY", amount: 70 });
  });
  it("charges EPay in CNY, with a built-in rate when none is configured", () => {
    expect(payableFor("epay", 10, cny)).toEqual({ currency: "CNY", amount: 70 });
    expect(payableFor("epay", 10, usd).amount).toBeCloseTo(72);
  });
});
