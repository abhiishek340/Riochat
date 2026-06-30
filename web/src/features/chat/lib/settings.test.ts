import { describe, expect, it } from "vitest";
import { isSendKey, parsePrefs, parseToolIds } from "./settings";

const key = (over: Partial<Parameters<typeof isSendKey>[0]> = {}) => ({ key: "Enter", shiftKey: false, altKey: false, ctrlKey: false, metaKey: false, ...over });

describe("parsePrefs", () => {
  it("defaults to on unless the value is exactly false", () => {
    const p = parsePrefs({ "chat.show_latency": "false", "chat.show_model_info": "nope" });
    expect(p.showLatency).toBe(false);
    expect(p.showModelInfo).toBe(true);
    expect(p.markdownRender).toBe(true);
    expect(p.deleteFilesByDefault).toBe(false);
    expect(parsePrefs({ "chat.delete_conversation_files_by_default": "true" }).deleteFilesByDefault).toBe(true);
  });

  it("reads presets with safe fallbacks", () => {
    expect(parsePrefs({ "chat.input_height": "loose", "chat.content_width": "wide" })).toMatchObject({ inputHeight: "loose", contentWidth: "wide" });
    expect(parsePrefs({ "chat.input_height": "huge" }).inputHeight).toBe("standard");
    expect(parsePrefs({ "chat.send_on_enter": "ctrl_enter" }).sendOn).toBe("modifier");
    expect(parsePrefs({ "chat.send_on_enter": "garbage" }).sendOn).toBe("enter");
  });

  it("parses default tool ids", () => {
    expect(parseToolIds("[3,1,3,0,-2,1.5,\"x\"]")).toEqual([3, 1]);
    expect(parseToolIds("nope")).toEqual([]);
  });
});

describe("isSendKey", () => {
  it("sends on plain Enter in enter mode, not with modifiers or shift", () => {
    expect(isSendKey(key(), "enter", false)).toBe(true);
    expect(isSendKey(key({ shiftKey: true }), "enter", false)).toBe(false);
    expect(isSendKey(key({ altKey: true }), "enter", false)).toBe(false);
    expect(isSendKey(key({ ctrlKey: true }), "enter", false)).toBe(false);
  });
  it("needs the platform modifier in modifier mode", () => {
    expect(isSendKey(key({ ctrlKey: true }), "modifier", false)).toBe(true);
    expect(isSendKey(key({ metaKey: true }), "modifier", false)).toBe(false);
    expect(isSendKey(key({ metaKey: true }), "modifier", true)).toBe(true);
    expect(isSendKey(key(), "modifier", false)).toBe(false);
  });
  it("never sends while composing", () => {
    expect(isSendKey(key({ isComposing: true }), "enter", false)).toBe(false);
    expect(isSendKey(key({ keyCode: 229 }), "enter", false)).toBe(false);
    expect(isSendKey(key({ key: "Process" }), "enter", false)).toBe(false);
  });
});
