import { describe, expect, it } from "vitest";
import { chatPreferencesFrom, normalizeSendShortcut, nativeModifierShortcut, parseChatSettings, parseToolIds } from "./chat-settings";

describe("parseChatSettings", () => {
  it("applies the defaults to an empty map", () => {
    const s = parseChatSettings({}, false);
    expect(s).toMatchObject({
      defaultModel: "",
      sendOnEnter: "enter",
      showTokenUsage: true,
      showModelInfo: true,
      showLatency: true,
      showBillingCost: true,
      showProcessTrace: true,
      markdownRender: true,
      autoExpandThinking: true,
      autoExpandToolCalls: true,
      autoGenerateTitle: true,
      autoGenerateLabels: true,
      deleteFilesByDefault: false,
      contextCompactAuto: false,
      restoreDraftOnFailure: true,
      inputHeight: "standard",
      contentWidth: "compact",
      fileMode: "auto",
    });
  });
  it("reads booleans by their default direction", () => {
    const s = parseChatSettings({ "chat.markdown_render": "false", "chat.delete_conversation_files_by_default": "true", "chat.context_compact_auto": "yes" }, false);
    expect(s.markdownRender).toBe(false);
    expect(s.deleteFilesByDefault).toBe(true);
    expect(s.contextCompactAuto).toBe(false);
  });
  it("falls back for unknown choices", () => {
    const s = parseChatSettings({ "chat.input_height": "huge", "chat.content_width": "full", "chat.file_mode": "x" }, false);
    expect([s.inputHeight, s.contentWidth, s.fileMode]).toEqual(["standard", "compact", "auto"]);
  });
});

describe("send shortcut", () => {
  it("maps either modifier variant to the platform's own", () => {
    expect(normalizeSendShortcut("meta_enter", false)).toBe("ctrl_enter");
    expect(normalizeSendShortcut("ctrl_enter", true)).toBe("meta_enter");
    expect(normalizeSendShortcut("enter", true)).toBe("enter");
    expect(normalizeSendShortcut("", true)).toBe("enter");
    expect(normalizeSendShortcut(undefined, false)).toBe("enter");
    expect(nativeModifierShortcut(true)).toBe("meta_enter");
  });
});

describe("chat preferences", () => {
  it("returns defaults until settings loaded", () => {
    expect(chatPreferencesFrom({ "chat.auto_generate_title": "false" }, false)).toEqual({
      autoGenerateTitle: true,
      autoGenerateLabels: true,
      autoExpandThinking: true,
      autoExpandToolCalls: true,
      deleteFilesByDefault: false,
      reuseModelOptions: true,
      loaded: false,
    });
    expect(chatPreferencesFrom({ "chat.auto_generate_title": "false" }, true).autoGenerateTitle).toBe(false);
  });
});

describe("parseToolIds", () => {
  it("keeps positive integers once and ignores junk", () => {
    expect(parseToolIds("[3,1,3,0,-2,1.5,\"x\"]")).toEqual([3, 1]);
    expect(parseToolIds("nope")).toEqual([]);
    expect(parseToolIds("{}")).toEqual([]);
    expect(parseToolIds(undefined)).toEqual([]);
  });
});
