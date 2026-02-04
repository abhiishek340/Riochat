import { describe, expect, it } from "vitest";
import { loginUrlFor, safeNextPath } from "./navigation";

describe("safeNextPath", () => {
  it("accepts same-origin paths only", () => {
    expect(safeNextPath("/files?q=1")).toBe("/files?q=1");
    for (const bad of ["//evil.com", "https://evil.com", "chat", "/\\evil", "/\t/evil.com", "/\n/evil.com", "/\u0000x", null, undefined]) {
      expect(safeNextPath(bad)).toBe("/chat");
    }
    expect(safeNextPath("x", "")).toBe("");
  });
  it("encodes the login redirect", () => {
    expect(loginUrlFor("/files?q=1")).toBe("/login?next=%2Ffiles%3Fq%3D1");
  });
});
