import { describe, expect, it } from "vitest";
import {
  appearanceWithPreset,
  initialTimezone,
  isAdminUser,
  isBootstrap,
  personalizePatch,
  planAccountSave,
  previousStep,
  storedPreset,
  timezoneChoices,
  validateAccount,
} from "./model";

const user = { username: "admin", displayName: "", mustResetPassword: false, initialUsernameRequired: false };

describe("validateAccount", () => {
  it("demands a new username when the initial one must change", () => {
    const u = { ...user, initialUsernameRequired: true, mustResetPassword: true };
    expect(validateAccount(u, { username: " Admin ", displayName: "", password: "abcdefgh" })).toBe("changeUsername");
    expect(validateAccount(u, { username: "ab", displayName: "", password: "abcdefgh" })).toBe("usernameShape");
    expect(validateAccount(u, { username: "root", displayName: "", password: "abcdefgh" })).toBe("usernameShape");
    expect(validateAccount(u, { username: "boss", displayName: "", password: "abc" })).toBe("password");
    expect(validateAccount(u, { username: "boss", displayName: "", password: "abcdefgh" })).toBeNull();
  });
  it("checks the display name for ordinary users", () => {
    expect(validateAccount(user, { username: "x", displayName: "ab", password: "" })).toBe("displayName");
    expect(validateAccount(user, { username: "x", displayName: "abc", password: "" })).toBeNull();
  });
  it("does not need a password outside the bootstrap case", () => {
    expect(validateAccount(user, { username: "x", displayName: "Alice", password: "" })).toBeNull();
  });
});

describe("planAccountSave", () => {
  it("saves the username only when it is required and the name only when it changed", () => {
    const u = { ...user, initialUsernameRequired: true, displayName: "Alice" };
    expect(planAccountSave(u, { username: " Boss ", displayName: " Alice ", password: "" })).toEqual({ username: "boss", displayName: null });
    expect(planAccountSave({ ...user, displayName: "Alice" }, { username: "x", displayName: "Alicia", password: "" })).toEqual({ username: null, displayName: "Alicia" });
  });
  it("never sends the bootstrap password or a display name", () => {
    const u = { ...user, mustResetPassword: true, initialUsernameRequired: true };
    const plan = planAccountSave(u, { username: "boss", displayName: "Boss Person", password: "secret-pass" });
    expect(plan).toEqual({ username: "boss", displayName: null });
    expect(isBootstrap(u)).toBe(true);
  });
});

describe("steps", () => {
  it("has no way back from the two-factor step or the first one", () => {
    expect(previousStep(1)).toBeNull();
    expect(previousStep(2)).toBe(1);
    expect(previousStep(3)).toBeNull();
    expect(previousStep(4)).toBe(3);
    expect(previousStep(6)).toBe(5);
  });
  it("recognises administrators", () => {
    expect(isAdminUser({ role: "admin" })).toBe(true);
    expect(isAdminUser({ role: "superadmin" })).toBe(true);
    expect(isAdminUser({ role: "user" })).toBe(false);
  });
});

describe("appearance", () => {
  it("replaces the preset and keeps or defaults the other keys", () => {
    expect(JSON.parse(appearanceWithPreset("", "moss"))).toEqual({ preset: "lagoon", chatFont: "default", chatFontWeight: "regular", fontSize: "standard" });
    const kept = appearanceWithPreset('{"preset":"tide","fontSize":"large","chatFont":"mono"}', "ember");
    expect(JSON.parse(kept)).toEqual({ preset: "ochre", chatFont: "mono", chatFontWeight: "regular", fontSize: "large" });
    expect(JSON.parse(appearanceWithPreset("not json", "dusk")).preset).toBe("ink");
  });
  it("reads the stored preset with a default", () => {
    expect(storedPreset('{"preset":"lagoon"}')).toBe("moss");
    expect(storedPreset('{"preset":"sepia"}')).toBe("ember");
    expect(storedPreset("")).toBe("tide");
  });
});

describe("time zone", () => {
  it("starts from the device on a first sign-in and from the profile otherwise", () => {
    expect(initialTimezone("", "Asia/Tokyo")).toBe("Asia/Tokyo");
    expect(initialTimezone("Etc/UTC", "Asia/Tokyo")).toBe("Asia/Tokyo");
    expect(initialTimezone("Europe/Paris", "Asia/Tokyo")).toBe("Europe/Paris");
  });
  it("sends the zone only when it differs from the profile (or UTC)", () => {
    expect(personalizePatch("Europe/Paris", "Europe/Paris")).toEqual({});
    expect(personalizePatch("", "Etc/UTC")).toEqual({});
    expect(personalizePatch("", "Asia/Tokyo")).toEqual({ timezone: "Asia/Tokyo" });
  });
  it("always offers the current zone in the list", () => {
    expect(timezoneChoices("Mars/Olympus")[0]).toBe("Mars/Olympus");
    expect(timezoneChoices("Etc/UTC").length).toBeGreaterThan(5);
  });
});
