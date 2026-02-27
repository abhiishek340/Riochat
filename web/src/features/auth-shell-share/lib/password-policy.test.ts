import { describe, expect, it } from "vitest";
import {
  codePointLength,
  digitsOnly,
  isValidDisplayName,
  isValidPassword,
  isValidUsername,
  normalizeUsername,
  sanitizeSecondFactorCode,
} from "./password-policy";

describe("isValidPassword", () => {
  it("requires 8 to 128 characters after trimming", () => {
    expect(isValidPassword("abcdefg")).toBe(false);
    expect(isValidPassword("abcdefgh")).toBe(true);
    expect(isValidPassword("  abcdefg  ")).toBe(false);
    expect(isValidPassword("a".repeat(128))).toBe(true);
    expect(isValidPassword("a".repeat(129))).toBe(false);
  });
  it("rejects digits-only passwords", () => {
    expect(isValidPassword("12345678")).toBe(false);
    expect(isValidPassword("1234567a")).toBe(true);
  });
  it("counts characters, not UTF-16 units", () => {
    expect(codePointLength("😀😀")).toBe(2);
    expect(isValidPassword("😀😀😀😀😀😀😀😀")).toBe(true);
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    expect(isValidPassword("\u5bc6\u7801\u5bc6\u7801\u5bc6\u7801\u5bc6")).toBe(false);
    expect(isValidPassword("\u5bc6\u7801\u5bc6\u7801\u5bc6\u7801\u5bc6\u7801")).toBe(true);
  });
});

describe("isValidUsername", () => {
  it("accepts 3-16 letters, digits, underscore and hyphen inside", () => {
    expect(isValidUsername("alice")).toBe(true);
    expect(isValidUsername("a_b-c")).toBe(true);
    expect(isValidUsername("Alice")).toBe(true); // lower-cased before checking
  });
  it("rejects bad edges, lengths, @ and reserved names", () => {
    expect(isValidUsername("ab")).toBe(false);
    expect(isValidUsername("a".repeat(17))).toBe(false);
    expect(isValidUsername("_abc")).toBe(false);
    expect(isValidUsername("abc-")).toBe(false);
    expect(isValidUsername("a@b.com")).toBe(false);
    expect(isValidUsername("admin")).toBe(false);
    expect(isValidUsername("Root")).toBe(false);
  });
  it("normalises by trimming and lower-casing", () => {
    expect(normalizeUsername("  BoB ")).toBe("bob");
  });
});

describe("display names and codes", () => {
  it("allows 3-16 characters", () => {
    expect(isValidDisplayName("ab")).toBe(false);
    expect(isValidDisplayName(" abc ")).toBe(true);
    expect(isValidDisplayName("a".repeat(17))).toBe(false);
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    expect(isValidDisplayName("\u5c0f\u660e\u540c\u5b66")).toBe(true);
  });
  it("strips non-digits from email codes and clamps length", () => {
    expect(digitsOnly("12a3-456789")).toBe("123456");
  });
  it("keeps letters, digits and hyphens for authenticator and recovery codes", () => {
    expect(sanitizeSecondFactorCode("ab12-CD 34!")).toBe("ab12-CD34");
    expect(sanitizeSecondFactorCode("x".repeat(40))).toHaveLength(32);
  });
});
