import { describe, expect, it } from "vitest";
import type { AuthUser } from "@/lib/api/types";
import { canEditUsername, classifyUsernameFailure, fieldsFromUser, pendingUsername, profileFieldsChanged, profilePatchBody, saveBlock } from "./profile-draft";

const user = (over: Partial<AuthUser> = {}): AuthUser =>
  ({
    username: "ada",
    displayName: " Ada L ",
    avatarURL: "",
    timezone: "",
    locale: "",
    profilePreferences: " likes tea ",
    ...over,
  }) as AuthUser;

describe("fieldsFromUser", () => {
  it("trims and fills the defaults", () => {
    expect(fieldsFromUser(user())).toEqual({ avatarURL: "", displayName: "Ada L", timezone: "Etc/UTC", locale: "en-US", profilePreferences: "likes tea" });
  });
});

describe("username editing", () => {
  it("is allowed only until the first change", () => {
    expect(canEditUsername(user())).toBe(true);
    expect(canEditUsername(user({ usernameChangedAt: "2025-01-01T00:00:00Z" }))).toBe(false);
    expect(canEditUsername(null)).toBe(false);
  });
  it("has a pending edit only when the normalised draft differs", () => {
    expect(pendingUsername(user(), "ada")).toBeNull();
    expect(pendingUsername(user(), "  ADA ")).toBeNull();
    expect(pendingUsername(user(), "")).toBeNull();
    expect(pendingUsername(user(), " Grace ")).toBe("grace");
    expect(pendingUsername(user({ usernameChangedAt: "x" }), "grace")).toBeNull();
  });
});

describe("profileFieldsChanged", () => {
  it("compares every field", () => {
    const base = fieldsFromUser(user());
    expect(profileFieldsChanged(base, base)).toBe(false);
    expect(profileFieldsChanged({ ...base, timezone: "Asia/Tokyo" }, base)).toBe(true);
    expect(profileFieldsChanged({ ...base, avatarURL: "generated:github:4" }, base)).toBe(true);
  });
});

describe("saveBlock", () => {
  it("checks the username first, then the display name", () => {
    expect(saveBlock({ pendingUsername: "admin", fieldsChanged: true, displayName: "ab" })).toBe("usernameInvalid");
    expect(saveBlock({ pendingUsername: null, fieldsChanged: true, displayName: "ab" })).toBe("displayNameInvalid");
    expect(saveBlock({ pendingUsername: "grace", fieldsChanged: true, displayName: "Grace H" })).toBeNull();
  });
  it("ignores a bad display name when only the username changes", () => {
    expect(saveBlock({ pendingUsername: "grace", fieldsChanged: false, displayName: "x" })).toBeNull();
  });
});

describe("classifyUsernameFailure", () => {
  it("maps statuses and the 'already used' phrase", () => {
    expect(classifyUsernameFailure({ status: 400 })).toBe("invalid");
    expect(classifyUsernameFailure({ status: 409, message: "username change already used" })).toBe("alreadyChanged");
    expect(classifyUsernameFailure({ status: 409, message: "username already exists" })).toBe("taken");
    expect(classifyUsernameFailure({ status: 409, errorCode: "user.username_change_used" })).toBe("alreadyChanged");
    expect(classifyUsernameFailure({ status: 500 })).toBe("other");
  });
});

describe("profilePatchBody", () => {
  it("sends all five fields together, trimmed, with the defaults for blank zone and locale", () => {
    expect(profilePatchBody({ avatarURL: " generated:github:4 ", displayName: " Ada ", timezone: " ", locale: "", profilePreferences: " tea\n" })).toEqual({
      avatarURL: "generated:github:4",
      displayName: "Ada",
      timezone: "Etc/UTC",
      locale: "en-US",
      profilePreferences: "tea",
    });
  });
});
