import { describe, expect, it } from "vitest";
import { challengeFor, constantTimeEqual, createPkcePair, randomToken } from "./pkce";

describe("PKCE", () => {
  it("matches the RFC 7636 example vector", async () => {
    expect(await challengeFor("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")).toBe("E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM");
  });
  it("produces unpadded base64url tokens of the expected length", () => {
    expect(randomToken(48)).toMatch(/^[A-Za-z0-9_-]{64}$/);
    expect(randomToken(32)).toMatch(/^[A-Za-z0-9_-]{43}$/);
    expect(randomToken(32)).not.toBe(randomToken(32));
  });
  it("builds a verifier, challenge and state", async () => {
    const pair = await createPkcePair();
    expect(pair.verifier).toHaveLength(64);
    expect(pair.state).toHaveLength(43);
    expect(pair.challenge).toBe(await challengeFor(pair.verifier));
  });
  it("compares strings by value", () => {
    expect(constantTimeEqual("abc", "abc")).toBe(true);
    expect(constantTimeEqual("abc", "abd")).toBe(false);
    expect(constantTimeEqual("abc", "abcd")).toBe(false);
    expect(constantTimeEqual("", "")).toBe(true);
  });
});
