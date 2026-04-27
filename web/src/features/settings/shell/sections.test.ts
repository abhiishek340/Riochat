import { describe, expect, it } from "vitest";
import { ALL_ENABLED, type Capabilities } from "@/lib/capabilities/features";
import { activeSection, isBlockedByCapability, isBlockedByPlatform, sectionForPath, visibleSections } from "./sections";

const flags = (over: Partial<Capabilities> = {}): Capabilities => ({ ...ALL_ENABLED, ...over });
const ids = (list: { id: string }[]) => list.map((s) => s.id);

describe("visibleSections", () => {
  it("keeps the fixed order", () => {
    const all = visibleSections({ flags: flags(), providerAccess: true, desktop: true });
    expect(ids(all)).toEqual(["general", "chat", "memory", "skills", "tools", "models", "subscription", "account", "network", "about"]);
  });
  it("hides Subscription without billingGating", () => {
    expect(ids(visibleSections({ flags: flags({ billingGating: false }), providerAccess: true, desktop: false }))).not.toContain("subscription");
  });
  it("shows Models only with multiUser and an enabled personal-provider answer", () => {
    const run = (f: Partial<Capabilities>, access: boolean | null) => ids(visibleSections({ flags: flags(f), providerAccess: access, desktop: false })).includes("models");
    expect(run({}, true)).toBe(true);
    expect(run({}, false)).toBe(false);
    expect(run({}, null)).toBe(false); // fail closed while unknown
    expect(run({ multiUser: false }, true)).toBe(false);
  });
  it("shows Network only in the desktop shell", () => {
    expect(ids(visibleSections({ flags: flags(), providerAccess: true, desktop: false }))).not.toContain("network");
  });
});

describe("route matching", () => {
  it("lets a section own its sub-paths", () => {
    expect(sectionForPath("/settings/chat")?.id).toBe("chat");
    expect(sectionForPath("/settings/chat/extra")?.id).toBe("chat");
    expect(sectionForPath("/settings/chatter")).toBeUndefined();
    expect(sectionForPath("/settings/account/")?.id).toBe("account");
  });
  it("highlights the default section for unknown paths", () => {
    expect(activeSection("/settings/nope").id).toBe("general");
  });
  it("honours a different base path", () => {
    expect(sectionForPath("/me/prefs/about", "/me/prefs")?.id).toBe("about");
  });
});

describe("guards", () => {
  it("blocks a path whose capability is off", () => {
    expect(isBlockedByCapability("/settings/subscription", flags({ billingGating: false }))).toBe(true);
    expect(isBlockedByCapability("/settings/subscription", flags())).toBe(false);
    expect(isBlockedByCapability("/settings/general", flags({ billingGating: false }))).toBe(false);
  });
  it("blocks the network page outside the desktop shell", () => {
    expect(isBlockedByPlatform("/settings/network", false)).toBe(true);
    expect(isBlockedByPlatform("/settings/network", true)).toBe(false);
    expect(isBlockedByPlatform("/settings/about", false)).toBe(false);
  });
});
