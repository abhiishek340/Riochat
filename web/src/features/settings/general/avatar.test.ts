import { describe, expect, it } from "vitest";
import { avatarInitial, fileAvatar, generatedAvatar, generatedPalette, parseAvatar } from "./avatar";

describe("parseAvatar", () => {
  it("recognises the three reference kinds", () => {
    expect(parseAvatar("")).toEqual({ kind: "empty" });
    expect(parseAvatar(" https://x.test/a.png ")).toEqual({ kind: "url", url: "https://x.test/a.png" });
    expect(parseAvatar("generated:github:12")).toEqual({ kind: "generated", variant: 12 });
    expect(parseAvatar("file:file_abc")).toEqual({ kind: "file", fileID: "file_abc" });
  });
  it("rejects malformed references", () => {
    expect(parseAvatar("generated:github:-1")).toEqual({ kind: "empty" });
    expect(parseAvatar("generated:github:x")).toEqual({ kind: "empty" });
    expect(parseAvatar("file:abc")).toEqual({ kind: "empty" });
  });
  it("builds references", () => {
    expect(generatedAvatar(7)).toBe("generated:github:7");
    expect(fileAvatar("file_1")).toBe("file:file_1");
  });
});

describe("avatarInitial", () => {
  it("uses the display name, then the username, then a question mark", () => {
    expect(avatarInitial("ada", "x")).toBe("A");
    expect(avatarInitial("  ", "grace")).toBe("G");
    expect(avatarInitial("", "")).toBe("?");
    expect(avatarInitial("𠮷x", "")).toBe("𠮷");
  });
});

describe("generatedPalette", () => {
  it("is deterministic and mirrors the pattern", () => {
    const a = generatedPalette(5);
    expect(generatedPalette(5)).toEqual(a);
    expect(a.cells).toHaveLength(25);
    for (let row = 0; row < 5; row++) {
      expect(a.cells[row * 5]).toBe(a.cells[row * 5 + 4]);
      expect(a.cells[row * 5 + 1]).toBe(a.cells[row * 5 + 3]);
    }
    expect(generatedPalette(6)).not.toEqual(a);
  });
});
