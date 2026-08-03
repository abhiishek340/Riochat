import { describe, expect, it } from "vitest";
import { isValidDecl, readDecl } from "./reasoning";
import { copySources, presetMatches, PRESETS } from "./presets";

describe("presets", () => {
  it("have unique ids and valid reasoning declarations", () => {
    expect(new Set(PRESETS.map((p) => p.id)).size).toBe(PRESETS.length);
    for (const p of PRESETS) {
      const decl = readDecl(p.caps.reasoning);
      if (p.caps.reasoning !== undefined) expect(decl && isValidDecl(decl), p.id).toBe(true);
    }
  });
  it("fit a model whose routes use the protocol", () => {
    const gemini = PRESETS.find((p) => p.id === "geminiThinking")!;
    expect(presetMatches(gemini, ["gemini_generate_content"])).toBe(true);
    expect(presetMatches(gemini, ["google_generate_content"])).toBe(true);
    expect(presetMatches(gemini, ["openai_responses"])).toBe(false);
    expect(presetMatches(PRESETS[0]!, [])).toBe(true);
  });
  it("lists other models with capabilities, searchable by name", () => {
    const models = [
      { id: 1, platformModelName: "Alpha", capabilitiesJSON: '{"a":1}' },
      { id: 2, platformModelName: "Beta", capabilitiesJSON: "{}" },
      { id: 3, platformModelName: "alphabet", capabilitiesJSON: '{"b":1}' },
      { id: 4, platformModelName: "Gamma", capabilitiesJSON: "" },
    ];
    expect(copySources(models, 1, "").map((m) => m.id)).toEqual([3]);
    expect(copySources(models, null, "ALPHA").map((m) => m.id)).toEqual([1, 3]);
  });
});
