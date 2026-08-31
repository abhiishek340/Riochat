import { describe, expect, it } from "vitest";
import { maskKey, parseImportFragment } from "./link-parser";

describe("single link", () => {
  it("parses the example link", () => {
    const r = parseImportFragment("#v=1&url=https://api.example.com/v1&key=sk-abcdefghijklmnop&name=Mine");
    expect(r).toEqual({
      ok: true,
      version: 1,
      entries: [{ baseURL: "https://api.example.com/v1", host: "api.example.com", key: "sk-abcdefghijklmnop", name: "Mine" }],
    });
  });
  it("accepts a missing version and a missing leading hash, decodes percent escapes and '+'", () => {
    const r = parseImportFragment("url=https%3A%2F%2FAPI.Example.com%2Fv1%2F&key=abc123&name=My+Key&protocol=anthropic_messages");
    expect(r.ok && r.entries[0]).toMatchObject({ baseURL: "https://api.example.com/v1", host: "api.example.com", name: "My Key", protocol: "anthropic_messages" });
  });
  it.each([
    ["", "empty"],
    ["v=3&url=https://a.test&key=k1", "unsupportedVersion"],
    ["url=http://a.test&key=k1", "insecureUrl"],
    ["url=ftp://a.test&key=k1", "invalidUrl"],
    ["url=https://u:p@a.test&key=k1", "invalidUrl"],
    ["url=https://a.test/x?y=1&key=k1", "invalidUrl"],
    ["url=https://a.test&key=k1&foo=1", "unknownParam"],
    ["url=https://a.test&url=https://b.test&key=k1", "repeatedParam"],
    ["key=k1", "missingUrl"],
    ["url=https://a.test", "missingKey"],
    ["url=https://a.test&key=has space", "invalidKey"],
    ["url=https://a.test&key=k1&protocol=nope", "invalidProtocol"],
    [`url=https://a.test&key=k1&name=${"x".repeat(65)}`, "invalidName"],
    ["url=%E0%A4%A&key=k1", "invalidUrl"],
  ])("rejects %j with %s", (fragment, reason) => {
    const r = parseImportFragment(fragment);
    expect(r.ok).toBe(false);
    if (!r.ok) expect(r.error.reason).toBe(reason);
  });
});

describe("fragment size", () => {
  it("counts bytes, not characters, against the 64 KiB limit", () => {
    // 22 000 three-byte characters are 66 000 bytes but only 22 000 characters.
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    const wide = "\u4e2d".repeat(22_000);
    const r = parseImportFragment(`url=https://a.test&key=k1&name=${wide}`);
    expect(r.ok).toBe(false);
  });
});

describe("multi-entry link", () => {
  it("starts an entry at each url", () => {
    const r = parseImportFragment("v=2&url=https://a.test&key=k1&name=A&url=https://b.test&key=k2");
    expect(r.ok && r.entries.map((e) => [e.host, e.name])).toEqual([["a.test", "A"], ["b.test", undefined]]);
  });
  it("rejects a field before the first url", () => {
    const r = parseImportFragment("v=2&key=k1&url=https://a.test");
    expect(!r.ok && r.error.reason).toBe("missingUrl");
  });
  it("names the entry for a duplicate address and key pair", () => {
    const r = parseImportFragment("v=2&url=https://a.test&key=k1&url=https://b.test&key=k2&url=https://b.test&key=k2");
    expect(r).toEqual({ ok: false, error: { reason: "duplicateEntry", entry: 3 } });
  });
  it("numbers the entry that fails", () => {
    const r = parseImportFragment("v=2&url=https://a.test&key=k1&url=http://b.test&key=k2");
    expect(r).toEqual({ ok: false, error: { reason: "insecureUrl", entry: 2 } });
  });
  it("rejects a repeated field inside an entry", () => {
    const r = parseImportFragment("v=2&url=https://a.test&key=k1&key=k2");
    expect(r).toEqual({ ok: false, error: { reason: "repeatedParam", entry: 1 } });
  });
  it("caps entries at 50", () => {
    const many = Array.from({ length: 51 }, (_, i) => `url=https://h${i}.test&key=k${i}`).join("&");
    const r = parseImportFragment(`v=2&${many}`);
    expect(!r.ok && r.error.reason).toBe("tooManyEntries");
  });
});

describe("maskKey", () => {
  it("shows 3 and 4 characters of long keys", () => {
    expect(maskKey("sk-abcdefghijklmnop")).toBe("sk-••••••••mnop");
  });
  it("shows only the last two of mid-length keys", () => {
    expect(maskKey("abcdefgh")).toBe("••••••gh");
    expect(maskKey("abcdef")).toMatch(/^•{4}ef$/);
  });
  it("shows nothing of short keys and clamps the bullets", () => {
    expect(maskKey("abc")).toBe("••••");
    expect(maskKey("a".repeat(11))).toBe(`${"•".repeat(8)}aa`);
    expect(maskKey("a".repeat(40))).toBe(`aaa${"•".repeat(8)}aaaa`);
  });
});
