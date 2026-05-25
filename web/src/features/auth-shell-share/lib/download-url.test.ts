import { describe, expect, it } from "vitest";
import { validDownloadUrl } from "./download-url";

describe("validDownloadUrl", () => {
  it("accepts plain http(s) addresses", () => {
    expect(validDownloadUrl(" https://example.com/get ")).toBe("https://example.com/get");
    expect(validDownloadUrl("http://example.com")).toBe("http://example.com");
  });
  it("rejects other schemes, credentials, blanks and long values", () => {
    expect(validDownloadUrl("ftp://x")).toBe("");
    expect(validDownloadUrl("javascript:alert(1)")).toBe("");
    expect(validDownloadUrl("https://u:p@host")).toBe("");
    expect(validDownloadUrl("")).toBe("");
    expect(validDownloadUrl(undefined)).toBe("");
    expect(validDownloadUrl(`https://example.com/${"a".repeat(200)}`)).toBe("");
    expect(validDownloadUrl(`https://example.com/${"a".repeat(179)}`)).not.toBe("");
  });
  it("allows exactly 200 characters", () => {
    const base = "https://example.com/";
    expect(validDownloadUrl(base + "a".repeat(200 - base.length))).toHaveLength(200);
    expect(validDownloadUrl(base + "a".repeat(201 - base.length))).toBe("");
  });
});
