import { describe, expect, it } from "vitest";
import { normalizeTrigger, validateSkill } from "./api";

describe("normalizeTrigger", () => {
  it("drops every leading slash and surrounding blanks", () => {
    expect(normalizeTrigger("/plan")).toBe("plan");
    expect(normalizeTrigger(" //plan ")).toBe("plan");
    expect(normalizeTrigger("a/b")).toBe("a/b");
  });
});

describe("validateSkill", () => {
  const ok = { title: "Plan", trigger: "/plan", description: "", markdown: "Do the thing" };
  it("accepts a complete skill", () => {
    expect(validateSkill(ok)).toBe(true);
  });
  it("needs a title, trigger and instructions", () => {
    expect(validateSkill({ ...ok, title: " " })).toBe(false);
    expect(validateSkill({ ...ok, trigger: "//" })).toBe(false);
    expect(validateSkill({ ...ok, markdown: "  " })).toBe(false);
  });
  it("enforces the length limits in characters", () => {
    expect(validateSkill({ ...ok, title: "x".repeat(65) })).toBe(false);
    expect(validateSkill({ ...ok, description: "x".repeat(257) })).toBe(false);
    expect(validateSkill({ ...ok, markdown: "x".repeat(10001) })).toBe(false);
    expect(validateSkill({ ...ok, title: "𠮷".repeat(64) })).toBe(true);
  });
});
