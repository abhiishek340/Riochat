import { describe, expect, it } from "vitest";
import { addRule, canAddRule, factorFromPercent, percentFromFactor, refreshAfter, removeRule } from "./rules";

describe("rate multiplier", () => {
  it("rounds a factor to whole percent", () => {
    expect(percentFromFactor("1.5")).toBe(150);
    expect(percentFromFactor(" 0.333 ")).toBe(33);
    expect(percentFromFactor("2")).toBe(200);
  });
  it("falls back to 100 for junk and non-positive values", () => {
    for (const bad of ["", "abc", "0", "-1", "0.001", "NaN"]) expect(percentFromFactor(bad), bad).toBe(100);
  });
  it("shows a stored percent as a factor, with 0 as 1", () => {
    expect(factorFromPercent(150)).toBe("1.5");
    expect(factorFromPercent(0)).toBe("1");
  });
});

describe("model access rules", () => {
  it("needs a value for everything but all", () => {
    expect(canAddRule([], { type: "vendor", value: " " })).toBe(false);
    expect(canAddRule([], { type: "vendor", value: "openai" })).toBe(true);
    expect(canAddRule([], { type: "all", value: "" })).toBe(true);
  });
  it("refuses duplicates", () => {
    const rules = [{ type: "vendor", value: "openai" }];
    expect(canAddRule(rules, { type: "vendor", value: "openai " })).toBe(false);
    expect(canAddRule(rules, { type: "protocol", value: "openai" })).toBe(true);
  });
  it("allows nothing once an all rule exists", () => {
    const rules = [{ type: "all", value: "" }];
    expect(canAddRule(rules, { type: "vendor", value: "x" })).toBe(false);
    expect(canAddRule(rules, { type: "all", value: "" })).toBe(false);
  });
  it("adds trimmed rules and removes by identity", () => {
    const added = addRule([], { type: "upstream", value: " 7 " });
    expect(added).toEqual([{ type: "upstream", value: "7" }]);
    expect(removeRule(added, { type: "upstream", value: "7" })).toEqual([]);
  });
});

describe("partial save", () => {
  it("reloads the list once the group saved and drops the cache once models were reached", () => {
    expect(refreshAfter({ group: false, models: false, users: false })).toEqual({ reloadList: false, invalidateCache: false });
    expect(refreshAfter({ group: true, models: false, users: false })).toEqual({ reloadList: true, invalidateCache: false });
    expect(refreshAfter({ group: true, models: true, users: false })).toEqual({ reloadList: true, invalidateCache: true });
  });
});
