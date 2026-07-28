import { describe, expect, it } from "vitest";
import type { OfficialItem } from "../billing/official";
import { AUTO_MARKER, bulkWindowProblem, groupDigits, openRouterWindow, parseWindowInput, removeWindow, resolveAutoWindow, sameIdentity, windowOf, writeAutoWindow, writeCustomWindow } from "./context";

describe("window input", () => {
  it("strips non-digits and reads the number", () => {
    expect(parseWindowInput("128,000")).toEqual({ kind: "value", value: 128000 });
    expect(parseWindowInput(" 1 000 000 ")).toEqual({ kind: "value", value: 1_000_000 });
  });
  it("treats empty as automatic and junk or out-of-range as invalid", () => {
    expect(parseWindowInput("")).toEqual({ kind: "auto" });
    expect(parseWindowInput("abc")).toEqual({ kind: "invalid" });
    expect(parseWindowInput("3000")).toEqual({ kind: "invalid" });
    expect(parseWindowInput("32000000")).toEqual({ kind: "invalid" });
    expect(parseWindowInput("4096")).toEqual({ kind: "value", value: 4096 });
    expect(parseWindowInput("16000000")).toEqual({ kind: "value", value: 16_000_000 });
  });
  it("groups digits for display", () => {
    expect(groupDigits(128000)).toBe("128,000");
    expect(groupDigits("1234567")).toBe("1,234,567");
    expect(groupDigits("12")).toBe("12");
  });
});

describe("writing the window", () => {
  const caps = { contextWindow: 8192, [AUTO_MARKER]: "AUTO", context_window: 1, contextWindowTokens: 2, context_window_tokens: 3, keep: true };
  it("writes a custom value without the marker or aliases", () => {
    expect(writeCustomWindow(caps, 128000)).toEqual({ contextWindow: 128000, keep: true });
  });
  it("writes a detected value with the automatic marker", () => {
    expect(writeAutoWindow(caps, 200000)).toEqual({ contextWindow: 200000, [AUTO_MARKER]: "auto", keep: true });
  });
  it("removes every trace of the window", () => {
    expect(removeWindow(caps)).toEqual({ keep: true });
  });
  it("tells automatic from custom values", () => {
    expect(windowOf(caps)).toEqual({ mode: "auto", value: 8192 });
    expect(windowOf({ contextWindow: 5000 })).toEqual({ mode: "custom", value: 5000 });
    expect(windowOf({})).toEqual({ mode: "none", value: null });
  });
});

describe("resolveAutoWindow", () => {
  const base = { catalog: null, openRouter: null, previousAuto: 64000, catalogConsulted: false, identityUnchanged: true };
  it("prefers the catalog, then OpenRouter", () => {
    expect(resolveAutoWindow({ ...base, catalog: 200000, openRouter: 100000 })).toBe(200000);
    expect(resolveAutoWindow({ ...base, openRouter: 100000 })).toBe(100000);
  });
  it("keeps the previous value only when no catalog was consulted and the identity is unchanged", () => {
    expect(resolveAutoWindow(base)).toBe(64000);
    expect(resolveAutoWindow({ ...base, identityUnchanged: false })).toBeNull();
    expect(resolveAutoWindow({ ...base, catalogConsulted: true })).toBeNull();
  });
  it("ignores detected values outside the valid range", () => {
    expect(resolveAutoWindow({ ...base, catalog: 100, catalogConsulted: true })).toBeNull();
  });
  it("compares identities with normalised text", () => {
    expect(sameIdentity({ name: " GPT-4o ", vendor: "OpenAI" }, { name: "gpt-4o", vendor: "openai" })).toBe(true);
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    expect(sameIdentity({ name: "\uff27\uff30\uff34", vendor: "x" }, { name: "gpt", vendor: "x" })).toBe(true);
    expect(sameIdentity({ name: "a", vendor: "x" }, { name: "a", vendor: "y" })).toBe(false);
  });
});

function item(id: string, contextLength: number | undefined, name = id): OfficialItem {
  return { id, canonicalSlug: id, name, contextLength, pricing: { prompt: "0", completion: "0" } };
}

describe("openRouterWindow", () => {
  it("matches an exact id", () => {
    expect(openRouterWindow("anthropic/claude-3.5-sonnet", "", [item("anthropic/claude-3.5-sonnet", 200000)])).toBe(200000);
  });
  it("matches after normalising punctuation", () => {
    expect(openRouterWindow("GPT_4o", "", [item("openai/gpt-4o", 128000, "OpenAI: GPT-4o")])).toBe(128000);
  });
  it("matches by the last path segment", () => {
    expect(openRouterWindow("claude-3.5-sonnet", "anthropic", [item("anthropic/claude-3.5-sonnet", 200000)])).toBe(200000);
  });
  it("disambiguates several candidates by the vendor's prefix", () => {
    const items = [item("anthropic/claude-x", 200000), item("amazon/claude-x", 100000)];
    expect(openRouterWindow("claude-x", "anthropic", items)).toBe(200000);
    expect(openRouterWindow("claude-x", "", items)).toBeNull();
  });
  it("does not guess among equals", () => {
    expect(openRouterWindow("model", "", [item("a/model", 10000), item("b/model", 20000)])).toBeNull();
  });
  it("only counts context lengths in range", () => {
    expect(openRouterWindow("a/m", "", [item("a/m", 100)])).toBeNull();
    expect(openRouterWindow("a/m", "", [item("a/m", undefined)])).toBeNull();
  });
});

describe("bulkWindowProblem", () => {
  const models = [{ platformModelName: "m1", capabilitiesJSON: '{"maxOutputTokens":8192}' }, { platformModelName: "m2", capabilitiesJSON: "" }];
  it("blocks an invalid value without naming a model", () => {
    expect(bulkWindowProblem(models, "32,000,000")).toEqual({ kind: "invalid" });
    expect(bulkWindowProblem(models, "")).toEqual({ kind: "invalid" });
  });
  it("blocks when a model's maximum output is not below the window", () => {
    expect(bulkWindowProblem(models, "8192")).toEqual({ kind: "maxOutput", model: "m1" });
    expect(bulkWindowProblem(models, "8193")).toBeNull();
  });
  it("blocks capabilities that are not an object", () => {
    expect(bulkWindowProblem([{ platformModelName: "arr", capabilitiesJSON: "[1,2]" }], "128000")).toEqual({ kind: "notObject", model: "arr" });
    expect(bulkWindowProblem([{ platformModelName: "bad", capabilitiesJSON: "{" }], "128000")).toEqual({ kind: "notObject", model: "bad" });
  });
  it("reads the snake case maximum too", () => {
    expect(bulkWindowProblem([{ platformModelName: "s", capabilitiesJSON: '{"max_output_tokens":9000}' }], "9000")).toEqual({ kind: "maxOutput", model: "s" });
  });
});
