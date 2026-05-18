import { describe, expect, it } from "vitest";
import { endOfDayIso, formatCents, formatCredit, formatDateTime, formatDisplayMoney, formatLatency, formatUsdTable, parseDateInput } from "./format";

describe("money and time display", () => {
  it("shows table USD with three decimals and grouping", () => {
    expect(formatUsdTable(1234.5, "en-US")).toBe("$1,234.500");
    expect(formatUsdTable(0.0004, "en-US")).toBe("$0.000");
    expect(formatUsdTable(null, "en-US")).toBe("-");
  });

  it("shows credit with up to two decimals and $0 when not positive", () => {
    expect(formatCredit(12, "en-US")).toBe("$12");
    expect(formatCredit(12.345, "en-US")).toBe("$12.35");
    expect(formatCredit(0, "en-US")).toBe("$0");
    expect(formatCredit(-4, "en-US")).toBe("$0");
  });

  it("formats cents with the currency code and falls back for unknown codes", () => {
    expect(formatCents(1999, "usd", "en-US")).toBe("$19.99");
    expect(formatCents(500, "not-a-code", "en-US")).toBe("5.00 NOT-A-CODE");
    expect(formatCents(500, "", "en-US")).toBe("$5.00");
  });

  it("converts to CNY only with a positive rate", () => {
    expect(formatDisplayMoney(2, { currency: "CNY", rate: 7.2 }, "en-US", 2)).toBe("¥14.40");
    expect(formatDisplayMoney(2, { currency: "CNY", rate: 0 }, "en-US", 2)).toBe("$2.00");
  });

  it("renders latency below a second in ms, otherwise in seconds", () => {
    expect(formatLatency(840, "en-US")).toBe("840 ms");
    expect(formatLatency(1500, "en-US")).toBe("1.5 s");
  });

  it("renders empty or invalid dates as a dash and rejects impossible calendar days", () => {
    expect(formatDateTime("", "en-US")).toBe("-");
    expect(formatDateTime("nope", "en-US")).toBe("-");
    expect(parseDateInput("2026-02-30")).toBeUndefined();
    expect(endOfDayIso("2026-02-30")).toBeUndefined();
  });
});
