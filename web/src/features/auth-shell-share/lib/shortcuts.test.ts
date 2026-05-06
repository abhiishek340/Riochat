import { describe, expect, it } from "vitest";
import { isApplePlatform, matchShortcut, modifierLabel, type KeyLike } from "./shortcuts";

const key = (over: Partial<KeyLike>): KeyLike => ({ key: "k", metaKey: false, ctrlKey: false, shiftKey: false, altKey: false, ...over });

describe("matchShortcut", () => {
  it("uses Cmd on Apple platforms and Ctrl elsewhere", () => {
    expect(matchShortcut(key({ metaKey: true }), true)).toBe("search");
    expect(matchShortcut(key({ ctrlKey: true }), true)).toBeNull();
    expect(matchShortcut(key({ ctrlKey: true }), false)).toBe("search");
    expect(matchShortcut(key({ metaKey: true }), false)).toBeNull();
  });
  it("starts a new chat with Shift+O and not with plain O", () => {
    expect(matchShortcut(key({ key: "O", ctrlKey: true, shiftKey: true }), false)).toBe("new-chat");
    expect(matchShortcut(key({ key: "o", ctrlKey: true }), false)).toBeNull();
  });
  it("keeps search off Shift+K", () => {
    expect(matchShortcut(key({ ctrlKey: true, shiftKey: true }), false)).toBeNull();
  });
  it("ignores IME composition and Alt chords", () => {
    expect(matchShortcut(key({ ctrlKey: true, isComposing: true }), false)).toBeNull();
    expect(matchShortcut(key({ ctrlKey: true, altKey: true }), false)).toBeNull();
  });
  it("labels the modifier", () => {
    expect(isApplePlatform("MacIntel")).toBe(true);
    expect(isApplePlatform("Win32")).toBe(false);
    expect(modifierLabel(true)).toBe("⌘");
    expect(modifierLabel(false)).toBe("Ctrl");
  });
});
