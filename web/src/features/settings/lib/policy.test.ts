import { describe, expect, it } from "vitest";
import {
  availableVerificationMethods,
  clipCodePoints,
  codePointLength,
  isCodeReady,
  isEmailBootstrap,
  isValidDisplayName,
  isValidPassword,
  isValidUsername,
  sanitizeCode,
  secondsLeft,
} from "./policy";

describe("username policy", () => {
  it("accepts lowercase names of 3 to 16 characters", () => {
    expect(isValidUsername("abc")).toBe(true);
    expect(isValidUsername("a-b_c9")).toBe(true);
    expect(isValidUsername("  Mixed_Case1 ")).toBe(true); // trimmed and lower-cased first
  });
  it("rejects bad shapes, '@' and reserved words", () => {
    expect(isValidUsername("ab")).toBe(false);
    expect(isValidUsername("a".repeat(17))).toBe(false);
    expect(isValidUsername("-abc")).toBe(false);
    expect(isValidUsername("abc_")).toBe(false);
    expect(isValidUsername("a@bc")).toBe(false);
    expect(isValidUsername("Admin")).toBe(false);
    expect(isValidUsername("settings")).toBe(false);
  });
});

describe("display name and password", () => {
  it("counts code points, not UTF-16 units", () => {
    expect(codePointLength("𠮷𠮷𠮷")).toBe(3);
    expect(isValidDisplayName("𠮷𠮷𠮷")).toBe(true);
    expect(isValidDisplayName("ab")).toBe(false);
    expect(isValidDisplayName(" ab ")).toBe(false);
    expect(isValidDisplayName("x".repeat(17))).toBe(false);
    expect(clipCodePoints("𠮷𠮷𠮷𠮷", 2)).toBe("𠮷𠮷");
  });
  it("needs 8 to 128 characters that are not all digits", () => {
    expect(isValidPassword("12345678")).toBe(false);
    expect(isValidPassword("1234567a")).toBe(true);
    expect(isValidPassword("short1")).toBe(false);
    expect(isValidPassword(`${"a".repeat(129)}`)).toBe(false);
    expect(isValidPassword("  abcdefg  ")).toBe(false); // 7 after trim
  });
});

describe("verification methods", () => {
  it("lists the authenticator first, then a verified mailbox", () => {
    expect(availableVerificationMethods({ twoFactorEnabled: true, emailVerificationEnabled: true, emailVerified: true })).toEqual(["two_factor", "email"]);
    expect(availableVerificationMethods({ twoFactorEnabled: false, emailVerificationEnabled: true, emailVerified: true })).toEqual(["email"]);
    expect(availableVerificationMethods({ twoFactorEnabled: false, emailVerificationEnabled: true, emailVerified: false })).toEqual(["none"]);
    expect(availableVerificationMethods({ twoFactorEnabled: false, emailVerificationEnabled: false, emailVerified: true })).toEqual(["none"]);
  });
  it("limits and validates codes per method", () => {
    expect(sanitizeCode("email", "12a3-45 6789")).toBe("123456");
    expect(sanitizeCode("two_factor", "ab-12 cd_34")).toBe("ab-12cd34");
    expect(sanitizeCode("two_factor", "x".repeat(40))).toHaveLength(32);
    expect(isCodeReady("email", "12345")).toBe(false);
    expect(isCodeReady("email", "123456")).toBe(true);
    expect(isCodeReady("two_factor", "12345")).toBe(false);
    expect(isCodeReady("two_factor", "ABCD-EFGH")).toBe(true);
    expect(isCodeReady("none", "")).toBe(true);
  });
});

describe("email bootstrap", () => {
  const base = { email: "a@b.co", emailSource: "", emailVerified: false, bootstrapUsed: false };
  it("is on without an address", () => {
    expect(isEmailBootstrap({ ...base, email: "" })).toBe(true);
  });
  it("is on for an unverified provider address never bootstrapped", () => {
    expect(isEmailBootstrap({ ...base, emailSource: "provider_unverified" })).toBe(true);
    expect(isEmailBootstrap({ ...base, emailSource: "provider_unverified", bootstrapUsed: true })).toBe(false);
    expect(isEmailBootstrap({ ...base, emailSource: "provider_unverified", emailVerified: true })).toBe(false);
    expect(isEmailBootstrap(base)).toBe(false);
  });
});

describe("secondsLeft", () => {
  it("rounds up and never goes negative", () => {
    expect(secondsLeft(10_500, 10_000)).toBe(1);
    expect(secondsLeft(70_000, 10_000)).toBe(60);
    expect(secondsLeft(5_000, 10_000)).toBe(0);
  });
});
