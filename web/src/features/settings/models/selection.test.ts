import { describe, expect, it } from "vitest";
import type { AvailableModel } from "./api";
import { buildRows, mergeAvailable, modelsChanged, preselect, toggleVisible, toRequestModels, visibleCheckState } from "./selection";

const models = (n: number): AvailableModel[] => Array.from({ length: n }, (_, i) => ({ name: `m${i}`, suggestedProtocols: ["openai_chat_completions"] }));

describe("preselect", () => {
  it("selects catalogs of 12 or fewer, none of larger ones", () => {
    expect(preselect(models(12))).toHaveLength(12);
    expect(preselect(models(13))).toHaveLength(0);
    expect(preselect(models(0))).toHaveLength(0);
    expect(preselect(models(2))[0]).toEqual({ name: "m0", protocols: ["openai_chat_completions"] });
  });
});

describe("toRequestModels", () => {
  const picked = [{ name: "a", protocols: ["p1", "p2"] }];
  it("sends bare names when the server has no per-model protocols", () => {
    expect(toRequestModels(picked, false)).toEqual(["a"]);
  });
  it("sends names with protocols otherwise", () => {
    expect(toRequestModels(picked, true)).toEqual([{ name: "a", protocols: ["p1", "p2"] }]);
  });
});

describe("modelsChanged", () => {
  const saved = [{ name: "a", protocols: ["x"] }, { name: "b", protocols: ["y", "z"] }];
  it("compares names and protocol lists", () => {
    expect(modelsChanged(saved, [{ name: "b", protocols: ["y", "z"] }, { name: "a", protocols: ["x"] }])).toBe(false);
    expect(modelsChanged(saved, [{ name: "a", protocols: ["x"] }])).toBe(true);
    expect(modelsChanged(saved, [{ name: "a", protocols: ["q"] }, { name: "b", protocols: ["y", "z"] }])).toBe(true);
  });
});

describe("mergeAvailable", () => {
  it("keeps enabled models the provider stopped listing", () => {
    const merged = mergeAvailable(models(1), [{ name: "gone", protocols: ["p"] }, { name: "m0", protocols: ["p"] }]);
    expect(merged.map((m) => m.name)).toEqual(["m0", "gone"]);
    expect(merged[1]?.suggestedProtocols).toEqual(["p"]);
  });
});

describe("buildRows", () => {
  it("lists models selected at open first, with their saved protocol", () => {
    const rows = buildRows(models(4), [{ name: "m2", protocols: ["saved"] }, { name: "m0", protocols: ["other"] }]);
    expect(rows.map((r) => r.name)).toEqual(["m2", "m0", "m1", "m3"]);
    expect(rows[0]).toEqual({ name: "m2", protocol: "saved", selected: true });
    expect(rows[2]).toEqual({ name: "m1", protocol: "openai_chat_completions", selected: false });
  });
});

describe("select all over the visible rows", () => {
  const rows = buildRows(models(3), []);
  it("reports all / some / none", () => {
    expect(visibleCheckState(rows, new Set())).toBe("none");
    expect(visibleCheckState(rows, new Set(["m0"]))).toBe("some");
    expect(visibleCheckState(rows, new Set(["m0", "m1", "m2"]))).toBe("all");
    expect(visibleCheckState([], new Set())).toBe("none");
  });
  it("selects the rest from a mixed state and clears from a full one", () => {
    expect([...toggleVisible(rows, new Set(["m0"]))].sort()).toEqual(["m0", "m1", "m2"]);
    expect([...toggleVisible(rows, new Set(["m0", "m1", "m2"]))]).toEqual([]);
  });
  it("leaves selections outside the filter alone", () => {
    const visible = rows.slice(0, 1);
    expect([...toggleVisible(visible, new Set(["m2"]))].sort()).toEqual(["m0", "m2"]);
  });
});
