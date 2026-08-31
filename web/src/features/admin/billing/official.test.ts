import { describe, expect, it } from "vitest";
import { convertOfficial, perMillion, searchCatalog, suggest, type OfficialItem } from "./official";

function item(id: string, over: Partial<OfficialItem> = {}, pricing: Partial<OfficialItem["pricing"]> = {}): OfficialItem {
  return { id, canonicalSlug: id, name: id, pricing: { prompt: "0.000003", completion: "0.000015", ...pricing }, ...over };
}

describe("convertOfficial", () => {
  it("scales per-token prices to per million tokens with a multiplier", () => {
    const r = convertOfficial(item("a/b"), 1.5);
    expect(r.ok && [r.value.input, r.value.output]).toEqual([4.5, 22.5]);
  });
  it("rounds to six decimals", () => {
    expect(perMillion("0.0000001234567", 1)).toBe(0.123457);
    expect(perMillion("", 1)).toBeNull();
    expect(perMillion("-1", 1)).toBeNull();
    expect(perMillion("abc", 1)).toBeNull();
  });
  it("is not applicable without a prompt or completion price", () => {
    const r = convertOfficial(item("a/b", {}, { completion: "" }), 1);
    expect(r).toEqual({ ok: false, missing: ["completion"] });
    expect(convertOfficial(item("a/b", {}, { prompt: "", completion: "" }), 1)).toEqual({ ok: false, missing: ["prompt", "completion"] });
  });
  it("fills the cache prices when given and leaves them at zero otherwise", () => {
    const r = convertOfficial(item("a/b", {}, { inputCacheRead: "0.0000003", inputCacheWrite: "0.00000375" }), 1);
    expect(r.ok && [r.value.cacheRead, r.value.cacheWrite]).toEqual([0.3, 3.75]);
    const bare = convertOfficial(item("a/b"), 1);
    expect(bare.ok && [bare.value.cacheRead, bare.value.cacheWrite]).toEqual([0, 0]);
  });
  it("turns rising overrides into tiers ending without a limit", () => {
    const r = convertOfficial(item("a/b", {}, { overrides: [{ minPromptTokens: 200000, prompt: "0.000006", completion: "0.0000225" }] }), 1);
    expect(r.ok && r.value.mode).toBe("tiered");
    expect(r.ok && r.value.tiers).toEqual([
      { upToTokens: "200000", input: "3", cacheRead: "0", cacheWrite: "0", output: "15" },
      { upToTokens: "0", input: "6", cacheRead: "0", cacheWrite: "0", output: "22.5" },
    ]);
  });
  it("marks everything-zero entries free and reports ignored fields", () => {
    const r = convertOfficial(item("a/b", {}, { prompt: "0", completion: "0", unsupportedFields: ["image"] }), 1);
    expect(r.ok && r.value.isFree).toBe(true);
    expect(r.ok && r.value.ignored).toEqual(["image"]);
    expect(convertOfficial(item("a/b"), 1).ok && (convertOfficial(item("a/b"), 1) as { value: { isFree: boolean } }).value.isFree).toBe(false);
  });
  it("ignores a non-positive multiplier", () => {
    const r = convertOfficial(item("a/b"), 0);
    expect(r.ok && r.value.input).toBe(3);
  });
});

describe("suggest", () => {
  const catalog = [
    item("openai/gpt-4o"),
    item("openai/gpt-4o-mini"),
    item("anthropic/claude-3.5-sonnet", { name: "Claude 3.5 Sonnet" }),
    item("google/gemini-2.0-flash"),
    item("anthropic/claude-3-opus"),
  ];
  it("ranks an exact id first", () => {
    expect(suggest("openai/gpt-4o", "", catalog)[0]?.item.id).toBe("openai/gpt-4o");
  });
  it("matches after normalisation", () => {
    expect(suggest("GPT_4o", "", catalog)[0]?.item.id).toBe("openai/gpt-4o");
  });
  it("matches a vendor-prefixed suffix and boosts the right vendor", () => {
    const hits = suggest("claude-3.5-sonnet", "anthropic", catalog);
    expect(hits[0]?.item.id).toBe("anthropic/claude-3.5-sonnet");
  });
  it("keeps fuzzy matches below exact ones", () => {
    const hits = suggest("gpt-4o", "", catalog);
    expect(hits[0]?.item.id).toBe("openai/gpt-4o");
    expect(hits[0]!.score).toBeGreaterThan(hits[1]!.score);
  });
  it("suggests nothing for unrelated names", () => {
    expect(suggest("zzz-unknown", "", catalog)).toEqual([]);
  });
});

describe("searchCatalog", () => {
  const catalog = [item("openai/gpt-4o"), item("anthropic/claude-3-opus", { name: "Claude 3 Opus" }), item("meta/llama-3-70b")];
  it("finds substrings in the id or name", () => {
    expect(searchCatalog("opus", catalog).map((i) => i.id)).toEqual(["anthropic/claude-3-opus"]);
    expect(searchCatalog("LLAMA", catalog).map((i) => i.id)).toEqual(["meta/llama-3-70b"]);
  });
  it("shows the first entries for an empty query", () => {
    expect(searchCatalog("", catalog, 2)).toHaveLength(2);
  });
});
