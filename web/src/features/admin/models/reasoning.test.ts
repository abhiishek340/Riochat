import { describe, expect, it } from "vitest";
import { declToValue, defaultLevel, isValidDecl, modalitiesValue, readDecl, sanitizeBudgets, sortLevels, translateLevel } from "./reasoning";

describe("defaultLevel", () => {
  it("prefers medium", () => {
    expect(defaultLevel(["low", "medium", "high"])).toBe("medium");
  });
  it("falls back to the highest level below medium", () => {
    expect(defaultLevel(["none", "minimal", "low", "high"])).toBe("low");
    expect(defaultLevel(["none", "minimal"])).toBe("minimal");
  });
  it("then the lowest real level, then the first level", () => {
    expect(defaultLevel(["none", "high", "max"])).toBe("high");
    expect(defaultLevel(["none"])).toBe("none");
    expect(defaultLevel([])).toBe("");
  });
});

describe("validity", () => {
  const ok = { format: "openai", levels: ["low", "medium"], default: "low" };
  it("accepts a coherent declaration", () => {
    expect(isValidDecl(ok)).toBe(true);
  });
  it("rejects unknown formats, empty or unknown levels and a stray default", () => {
    expect(isValidDecl({ ...ok, format: "mystery" })).toBe(false);
    expect(isValidDecl({ ...ok, levels: [] })).toBe(false);
    expect(isValidDecl({ ...ok, levels: ["low", "extreme"] })).toBe(false);
    expect(isValidDecl({ ...ok, default: "high" })).toBe(false);
  });
});

describe("translateLevel", () => {
  it("sends level names for level formats", () => {
    expect(translateLevel("openai", "high")).toBe("high");
    expect(translateLevel("gemini_level", "none")).toBe("none");
  });
  it("disables anthropic effort for none", () => {
    expect(translateLevel("anthropic_effort", "none")).toBe("disabled");
    expect(translateLevel("anthropic_effort", "max")).toBe("max");
  });
  it("sends token budgets for budget formats", () => {
    expect(translateLevel("gemini_budget", "none")).toBe("0");
    expect(translateLevel("gemini_budget", "low", { low: 1024 })).toBe("1024");
    expect(translateLevel("anthropic_budget", "none")).toBe("disabled");
    expect(translateLevel("anthropic_budget", "high", { high: 8000 })).toBe("8000");
  });
  it("qwen and toggle send enabled or disabled unless a budget is set", () => {
    expect(translateLevel("qwen", "none")).toBe("disabled");
    expect(translateLevel("qwen", "low")).toBe("enabled");
    expect(translateLevel("qwen", "low", { low: 512 })).toBe("512");
    expect(translateLevel("toggle", "medium")).toBe("enabled");
    expect(translateLevel("toggle", "none")).toBe("disabled");
  });
});

describe("stored value", () => {
  it("orders levels canonically and fills a default", () => {
    expect(declToValue({ format: "openai", levels: ["high", "low"], default: "" })).toEqual({ format: "openai", levels: ["low", "high"], default: "low" });
    expect(sortLevels(["ultra", "none", "low"])).toEqual(["none", "low", "ultra"]);
  });
  it("keeps budgets only for budget formats and selected levels", () => {
    expect(sanitizeBudgets("openai", ["low"], { low: 100 })).toBeUndefined();
    expect(sanitizeBudgets("gemini_budget", ["low"], { low: 100, high: 5 })).toEqual({ low: 100 });
    expect(declToValue({ format: "gemini_budget", levels: ["low", "high"], default: "low", budgets: { low: 100, medium: 7 } }).budgets).toEqual({ low: 100 });
  });
  it("reads a stored declaration", () => {
    expect(readDecl({ format: "qwen", levels: ["low"], default: "low", budgets: { low: 5 } })).toEqual({ format: "qwen", levels: ["low"], default: "low", budgets: { low: 5 } });
    expect(readDecl([])).toBeNull();
  });
  it("always includes text among the input modalities", () => {
    expect(modalitiesValue(["image", "bogus", "pdf"])).toEqual(["text", "image", "pdf"]);
    expect(modalitiesValue([])).toEqual(["text"]);
  });
});
