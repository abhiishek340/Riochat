import { describe, expect, it } from "vitest";
import { avatarSrc, generatedAvatar, identicon } from "./avatar";

describe("avatar", () => {
  it("draws the same picture for the same seed and variant", () => {
    expect(identicon("u1", 7)).toBe(identicon("u1", 7));
    expect(identicon("u1", 7)).not.toBe(identicon("u1", 8));
    expect(identicon("u1", 7).startsWith("data:image/svg+xml")).toBe(true);
  });
  it("resolves the reference forms", () => {
    expect(avatarSrc("generated:github:3", { publicID: "p" })).toBe(identicon("p", 3));
    expect(avatarSrc("", { username: "amy" })).toBe(identicon("amy", 0));
    expect(avatarSrc("https://x.test/a.png", {})).toBe("https://x.test/a.png");
    expect(avatarSrc("file:file_abc", { publicID: "pub" })).toContain("/users/pub/avatar?file=file_abc");
    expect(avatarSrc("file:file_abc", {})).toBe("file:file_abc");
  });
  it("makes whole-number variants", () => {
    expect(generatedAvatar(5)).toBe("generated:github:5");
    expect(generatedAvatar()).toMatch(/^generated:github:\d+$/);
  });
});
