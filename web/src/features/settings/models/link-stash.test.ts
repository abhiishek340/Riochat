// @vitest-environment jsdom
import { afterEach, describe, expect, it } from "vitest";
import { clearStash, readStash, saveStash, STASH_KEY, STASH_TTL_MS } from "./link-stash";

afterEach(() => window.sessionStorage.clear());

describe("link stash", () => {
  it("is read without being consumed", () => {
    saveStash("v=1&url=x", 1_000);
    expect(readStash(2_000)).toBe("v=1&url=x");
    expect(readStash(3_000)).toBe("v=1&url=x");
  });
  it("expires after ten minutes", () => {
    saveStash("frag", 0);
    expect(readStash(STASH_TTL_MS)).toBe("frag");
    expect(readStash(STASH_TTL_MS + 1)).toBe("");
  });
  it("ignores malformed content", () => {
    window.sessionStorage.setItem(STASH_KEY, "{not json");
    expect(readStash()).toBe("");
    window.sessionStorage.setItem(STASH_KEY, JSON.stringify({ fragment: 3 }));
    expect(readStash()).toBe("");
  });
  it("can be cleared", () => {
    saveStash("frag");
    clearStash();
    expect(readStash()).toBe("");
  });
});
