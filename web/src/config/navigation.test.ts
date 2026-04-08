import { describe, expect, it } from "vitest";
import { ADMIN_NAV } from "./navigation";

const entry = (id: string) => ADMIN_NAV.flatMap((g) => g.items).find((e) => e.id === id);

describe("admin navigation", () => {
  it("never hides the default section, which every other redirect lands on", () => {
    expect(entry("statistics")?.feature).toBeUndefined();
    expect(entry("statistics")?.role).toBeUndefined();
  });

  it("follows the section table for feature flags", () => {
    expect(entry("users")?.feature).toBe("multiUser");
    expect(entry("groups")?.feature).toBe("multiUser");
    expect(entry("user-keys")?.feature).toBe("multiUser");
    expect(entry("announcements")?.feature).toBe("announcements");
    expect(entry("content-moderation")?.feature).toBe("contentModeration");
    expect(entry("login")?.feature).toBe("identityProviders");
  });

  it("keeps billing visible because only its sub-sections depend on billingGating", () => {
    expect(entry("billing")?.feature).toBeUndefined();
  });

  it("lists content moderation for super administrators only", () => {
    expect(entry("content-moderation")?.role).toBe("superadmin");
    const others = ADMIN_NAV.flatMap((g) => g.items).filter((e) => e.id !== "content-moderation");
    expect(others.every((e) => e.role === undefined)).toBe(true);
  });

  it("keeps the knowledge-base entry listed so an administrator can switch the feature back on", () => {
    expect(entry("knowledge-bases")?.policy).toBeUndefined();
    expect(entry("knowledge-bases")?.feature).toBeUndefined();
  });

  it("lists the sixteen sections in the order of the console's section table", () => {
    expect(ADMIN_NAV.flatMap((g) => g.items).map((e) => e.id)).toEqual([
      "statistics",
      "users",
      "groups",
      "upstreams",
      "models",
      "tools",
      "billing",
      "announcements",
      "logs",
      "content-moderation",
      "login",
      "conversation",
      "files",
      "knowledge-bases",
      "user-keys",
      "about",
    ]);
  });
});
