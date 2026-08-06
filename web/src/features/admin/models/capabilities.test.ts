import { describe, expect, it } from "vitest";
import {
  cacheEditable,
  catalogForProtocols,
  addCatalogTool,
  controlRows,
  getPath,
  imageToggleShown,
  nativeRows,
  parseCaps,
  parseDefaultValue,
  readImageStream,
  readModalities,
  readPromptCache,
  rowFromCatalog,
  setPath,
  splitOptions,
  stringifyCaps,
  unsetPath,
  validateControls,
  validateNative,
  validPath,
  writeControls,
  writeImageStream,
  writeModalities,
  writeNative,
  writePromptCache,
  type ControlRow,
  type NativeToolRow,
} from "./capabilities";

describe("parsing", () => {
  it("treats empty text and {} as no capabilities", () => {
    expect(parseCaps("")).toEqual({ ok: true, value: {} });
    expect(parseCaps("  {}  ")).toEqual({ ok: true, value: {} });
    expect(stringifyCaps({})).toBe("");
    expect(stringifyCaps({ a: 1 })).toBe('{"a":1}');
  });
  it("rejects text that is not an object", () => {
    expect(parseCaps("[1,2]")).toEqual({ ok: false });
    expect(parseCaps("{")).toEqual({ ok: false });
    expect(parseCaps("3")).toEqual({ ok: false });
  });
});

describe("dotted paths", () => {
  it("validates segments", () => {
    for (const bad of ["", " ", "a..b", ".a", "a."]) expect(validPath(bad), bad).toBe(false);
    expect(validPath("reasoning.effort")).toBe(true);
  });
  it("sets and reads nested values without touching the original", () => {
    const base = { a: { x: 1 } };
    const next = setPath(base, "a.y.z", 5);
    expect(next).toEqual({ a: { x: 1, y: { z: 5 } } });
    expect(base).toEqual({ a: { x: 1 } });
    expect(getPath(next, "a.y.z")).toBe(5);
    expect(getPath(next, "a.q")).toBeUndefined();
  });
  it("unsets and prunes empty parents", () => {
    expect(unsetPath({ a: { b: { c: 1 } }, k: 2 }, "a.b.c")).toEqual({ k: 2 });
    expect(unsetPath({ a: { b: 1, c: 2 } }, "a.b")).toEqual({ a: { c: 2 } });
    expect(unsetPath({ a: 1 }, "x.y")).toEqual({ a: 1 });
  });
  it("reads default values as JSON when possible", () => {
    expect(parseDefaultValue("0.5")).toBe(0.5);
    expect(parseDefaultValue("true")).toBe(true);
    expect(parseDefaultValue("high")).toBe("high");
    expect(parseDefaultValue("  ")).toBeUndefined();
  });
  it("splits and de-duplicates select options", () => {
    expect(splitOptions("a, b,,a , c")).toEqual(["a", "b", "c"]);
  });
});

const row = (over: Partial<ControlRow> = {}): ControlRow => ({ path: "reasoning.effort", label: "Effort", description: "", type: "select", options: "low, high", defaultValue: "low", locked: false, ...over });

describe("option controls", () => {
  it("flags each problem on its row", () => {
    expect(validateControls([row({ path: "a..b" })])).toEqual([{ index: 0, issue: "path" }]);
    expect(validateControls([row({ type: "" })])).toEqual([{ index: 0, issue: "type" }]);
    expect(validateControls([row({ options: " , " })])).toEqual([{ index: 0, issue: "options" }]);
    expect(validateControls([row({ locked: true, defaultValue: "" })])).toEqual([{ index: 0, issue: "locked" }]);
    expect(validateControls([row(), row()])).toEqual([{ index: 1, issue: "duplicate" }]);
    expect(validateControls([row()])).toEqual([]);
  });
  it("writes controls, defaults and locks and keeps unrelated keys", () => {
    const out = writeControls({ keepMe: 1, defaultOptions: { other: 7 } }, [row({ locked: true })]);
    expect(out).toEqual({
      keepMe: 1,
      defaultOptions: { other: 7, reasoning: { effort: "low" } },
      optionControls: [{ path: "reasoning.effort", type: "select", label: "Effort", options: ["low", "high"] }],
      lockedOptionPaths: ["reasoning.effort"],
    });
  });
  it("removes managed paths that no longer have a row", () => {
    const first = writeControls({}, [row()]);
    const again = writeControls(first, []);
    expect(again).toEqual({});
  });
  it("reads rows back, including defaults and locks", () => {
    const written = writeControls({}, [row({ locked: true }), row({ path: "temperature", type: "number", options: "", defaultValue: "0.7" })]);
    const rows = controlRows(written);
    expect(rows.map((r) => [r.path, r.type, r.defaultValue, r.locked])).toEqual([
      ["reasoning.effort", "select", "low", true],
      ["temperature", "number", "0.7", false],
    ]);
  });
  it("normalises {} back to an empty string", () => {
    expect(stringifyCaps(writeControls({}, []))).toBe("");
  });
});

const tool = (over: Partial<NativeToolRow> = {}): NativeToolRow => ({ key: "web_search", protocols: ["openai_responses"], type: "web_search", label: "Web search", description: "", provider: "openai", enabled: true, defaultEnabled: false, payload: '{"type":"web_search"}', ...over });

describe("native tools", () => {
  it("validates enabled rows only", () => {
    expect(validateNative([tool()])).toEqual([]);
    expect(validateNative([tool({ key: " " })])).toEqual([{ index: 0, issue: "key" }]);
    expect(validateNative([tool({ protocols: [] })])).toEqual([{ index: 0, issue: "protocols" }]);
    expect(validateNative([tool({ payload: "[1]" })])).toEqual([{ index: 0, issue: "payload" }]);
    expect(validateNative([tool({ payload: "{" })])).toEqual([{ index: 0, issue: "payload" }]);
    expect(validateNative([tool({ type: "", payload: "{}" })])).toEqual([{ index: 0, issue: "type" }]);
    expect(validateNative([tool({ type: "", payload: '{"type":"x"}' })])).toEqual([]);
    expect(validateNative([tool({ enabled: false, key: "" })])).toEqual([]);
  });
  it("migrates legacy keys into rows and drops them on write", () => {
    const rows = nativeRows({ nativeToolKeys: ["a", "b"] });
    expect(rows.map((r) => r.key)).toEqual(["a", "b"]);
    const written = writeNative({ nativeToolKeys: ["a", "b"], keep: 1 }, [tool()]);
    expect(written.nativeToolKeys).toBeUndefined();
    expect(written.keep).toBe(1);
    expect(written.nativeTools).toEqual([{ key: "web_search", protocols: ["openai_responses"], label: "Web search", enabled: true, defaultEnabled: false, payload: { type: "web_search" }, type: "web_search", provider: "openai" }]);
  });
  it("matches catalog tools to route protocols, with the google spelling normalised", () => {
    const catalog = [
      { toolKey: "a", type: "t", provider: "g", label: "B", description: "", protocol: "gemini_generate_content", payload: {} },
      { toolKey: "b", type: "t", provider: "o", label: "A", description: "", protocol: "openai_responses", payload: { type: "x" } },
    ];
    expect(catalogForProtocols(catalog, ["google_generate_content"]).map((t) => t.toolKey)).toEqual(["a"]);
    expect(catalogForProtocols(catalog, ["openai_responses", "google_generate_content"]).map((t) => t.toolKey)).toEqual(["b", "a"]);
    expect(rowFromCatalog(catalog[1]!).payload).toBe('{\n  "type": "x"\n}');
  });
});

describe("adding catalog tools", () => {
  const entry = { toolKey: "openai.web_search", type: "web_search", provider: "OpenAI", label: "Web search", description: "", protocol: "openai_responses", payload: { type: "web_search" }, defaultEnabled: true };
  it("appends a new tool and merges protocols of a known one", () => {
    const one = addCatalogTool([], entry);
    expect(one).toHaveLength(1);
    expect(one[0]).toMatchObject({ key: "openai.web_search", protocols: ["openai_responses"], defaultEnabled: true });
    const two = addCatalogTool(one, { ...entry, protocol: "openai_chat_completions" });
    expect(two).toHaveLength(1);
    expect(two[0]?.protocols).toEqual(["openai_responses", "openai_chat_completions"]);
    expect(addCatalogTool(two, entry)[0]?.protocols).toEqual(["openai_responses", "openai_chat_completions"]);
  });
});

describe("prompt cache", () => {
  it("reads availability and mode", () => {
    expect(readPromptCache({})).toEqual({ availability: "auto", explicit: false });
    expect(readPromptCache({ promptCache: { enabled: true, mode: "explicit", ttl: "30m" } })).toEqual({ availability: "enabled", explicit: true });
    expect(readPromptCache({ promptCache: { enabled: false, mode: "explicit" } })).toEqual({ availability: "disabled", explicit: false });
  });
  it("writes auto as nothing, explicit with a ttl, disabled without mode", () => {
    expect(writePromptCache({ promptCache: { retention: "1h" } }, { availability: "auto", explicit: false })).toEqual({});
    expect(writePromptCache({}, { availability: "enabled", explicit: true })).toEqual({ promptCache: { enabled: true, mode: "explicit", ttl: "30m" } });
    expect(writePromptCache({}, { availability: "auto", explicit: true })).toEqual({ promptCache: { mode: "explicit", ttl: "30m" } });
    expect(writePromptCache({}, { availability: "disabled", explicit: true })).toEqual({ promptCache: { enabled: false } });
  });
  it("is editable only for OpenAI chat or responses routes", () => {
    expect(cacheEditable(["openai_responses"])).toBe(true);
    expect(cacheEditable(["anthropic_messages"])).toBe(false);
  });
});

describe("image stream and modalities", () => {
  it("streams unless turned off", () => {
    expect(readImageStream({})).toBe(true);
    expect(readImageStream({ image: { stream: false } })).toBe(false);
    expect(writeImageStream({}, false)).toEqual({ image: { stream: false } });
    expect(writeImageStream({ image: { stream: false, other: 1 } }, true)).toEqual({ image: { other: 1 } });
    expect(writeImageStream({ image: { stream: false } }, true)).toEqual({});
    expect(imageToggleShown(["xai_image"])).toBe(true);
    expect(imageToggleShown(["openai_responses"])).toBe(false);
  });
  it("follows the catalog unless set, and always includes text", () => {
    expect(readModalities({})).toEqual({ mode: "auto", chosen: [] });
    expect(writeModalities({ inputModalities: ["text"] }, "auto", ["image"])).toEqual({});
    expect(writeModalities({}, "custom", ["pdf", "image"])).toEqual({ inputModalities: ["text", "image", "pdf"] });
  });
});
