import { describe, expect, it } from "vitest";
import { finalizeCaps, needsOpenRouter } from "./finalize";

const same = { name: "gpt-5", vendor: "openai" };
const base = { existing: same, form: same, openRouter: null as number | null };

describe("finalizeCaps", () => {
  it("leaves a custom window alone", () => {
    const caps = { contextWindow: 50000 };
    expect(finalizeCaps({ ...base, caps, catalog: { consulted: true, window: 200000 } })).toBe(caps);
  });
  it("stores the catalog value with the automatic marker", () => {
    expect(finalizeCaps({ ...base, caps: { a: 1 }, catalog: { consulted: true, window: 200000 } })).toEqual({ a: 1, contextWindow: 200000, _riochatContextWindowMode: "auto" });
  });
  it("falls back to OpenRouter when the catalog has nothing", () => {
    expect(finalizeCaps({ ...base, caps: {}, catalog: { consulted: true, window: null }, openRouter: 128000 })).toMatchObject({ contextWindow: 128000 });
  });
  it("keeps the previous automatic value only if no catalog was consulted and the identity is the same", () => {
    const caps = { contextWindow: 64000, _riochatContextWindowMode: "auto" };
    expect(finalizeCaps({ ...base, caps, catalog: { consulted: false, window: null } })).toEqual(caps);
    expect(finalizeCaps({ ...base, caps, form: { name: "GPT-5 ", vendor: "OpenAI" }, catalog: { consulted: false, window: null } })).toEqual(caps);
    expect(finalizeCaps({ ...base, caps, form: { name: "other", vendor: "openai" }, catalog: { consulted: false, window: null } })).toEqual({});
    expect(finalizeCaps({ ...base, caps, existing: null, catalog: { consulted: false, window: null } })).toEqual({});
  });
  it("drops a stale automatic value when a catalog was consulted and had nothing", () => {
    const caps = { contextWindow: 64000, _riochatContextWindowMode: "auto", keep: true };
    expect(finalizeCaps({ ...base, caps, catalog: { consulted: true, window: null } })).toEqual({ keep: true });
  });
  it("asks OpenRouter only in automatic mode without a catalog value", () => {
    expect(needsOpenRouter({}, null)).toBe(true);
    expect(needsOpenRouter({}, 1000)).toBe(false);
    expect(needsOpenRouter({ contextWindow: 9000 }, null)).toBe(false);
  });
});
