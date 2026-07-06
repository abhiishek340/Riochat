import { describe, expect, it } from "vitest";
import type { UserRecord } from "../lib/wire";
import {
  canManage,
  createBody,
  dayOf,
  defaultTier,
  editFormOf,
  filterUsers,
  parseBalance,
  planEdit,
  roleChoices,
  sortUsers,
  tierChoices,
  validateCreate,
  validateDisplayName,
  validatePassword,
  validateUsername,
  type CreateForm,
} from "./rules";

function user(over: Partial<UserRecord> = {}): UserRecord {
  return {
    id: 1, publicID: "p", username: "alice", displayName: "Alice", email: "a@x.test", phone: "", avatarURL: "", role: "user", status: "active", timezone: "Etc/UTC", locale: "en-US",
    profilePreferences: "", appearancePreferences: "", subscriptionTier: "free", subscriptionPlanName: "", subscriptionStatus: "", subscriptionExpiresAt: null, billingAccountStatus: "active",
    billingAccountCurrency: "USD", billingBalanceUSD: 1.5, billingBalanceNanousd: 1_500_000_000, emailVerifiedAt: null, phoneVerifiedAt: null, lastLoginAt: null, lastActiveAt: null,
    createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z", identityProviders: [], twoFactorEnabled: false, twoFactorAvailable: true, twoFactorRequired: false, twoFactorRecoveryCount: 0, ...over,
  };
}

const createForm: CreateForm = { avatarURL: "generated:github:1", username: "good_user", displayName: "", password: "abcd1234", email: "", timezone: "", locale: "", tier: "free", expiryDate: "" };

describe("create validation", () => {
  it("checks the username", () => {
    expect(validateUsername("ab")?.key).toBe("adminUsers.problems.usernameLength");
    expect(validateUsername("a".repeat(17))?.key).toBe("adminUsers.problems.usernameLength");
    expect(validateUsername("Admin")?.key).toBe("adminUsers.problems.usernameReserved");
    expect(validateUsername("a@b.com")?.key).toBe("adminUsers.problems.usernameShape");
    expect(validateUsername("_bad")?.key).toBe("adminUsers.problems.usernameShape");
    expect(validateUsername("bad-")?.key).toBe("adminUsers.problems.usernameShape");
    expect(validateUsername("  Good_User ")).toBeNull();
  });
  it("counts display name characters, not units", () => {
    expect(validateDisplayName("")).toBeNull();
    expect(validateDisplayName("ab")).not.toBeNull();
    expect(validateDisplayName("😀😀😀")).toBeNull();
    expect(validateDisplayName("", true)).not.toBeNull();
  });
  it("rejects digit-only and out-of-range passwords", () => {
    expect(validatePassword("12345678")?.key).toBe("adminUsers.problems.passwordDigits");
    expect(validatePassword("abc")?.key).toBe("adminUsers.problems.passwordLength");
    expect(validatePassword("abcd1234")).toBeNull();
  });
  it("needs an expiry for a paid tier in period mode only", () => {
    const paid = { ...createForm, tier: "pro" };
    expect(validateCreate(paid, "period")?.key).toBe("adminUsers.problems.expiryRequired");
    expect(validateCreate(paid, "usage")).toBeNull();
    expect(validateCreate({ ...paid, expiryDate: "2030-02-31" }, "period")?.key).toBe("adminUsers.problems.expiryInvalid");
    expect(validateCreate({ ...paid, expiryDate: "2030-01-01" }, "period")).toBeNull();
  });
  it("sends trimmed values and the end of the chosen day", () => {
    const body = createBody({ ...createForm, username: " Good_User ", password: " abcd1234 ", tier: "pro", expiryDate: "2030-01-01" }, "period");
    expect(body.username).toBe("good_user");
    expect(body.password).toBe("abcd1234");
    expect(body.timezone).toBe("Etc/UTC");
    expect(body.locale).toBe("en-US");
    expect(body.subscriptionExpiresAt).toBe(new Date(2030, 0, 1, 23, 59, 59, 999).toISOString());
  });
  it("leaves subscription fields out of other modes and for free", () => {
    expect(createBody(createForm, "self").subscriptionTier).toBeUndefined();
    const free = createBody(createForm, "period");
    expect(free.subscriptionTier).toBe("free");
    expect(free.subscriptionExpiresAt).toBeUndefined();
    expect(defaultTier([])).toBe("free");
    expect(defaultTier(["basic", "pro"])).toBe("basic");
  });
});

describe("planEdit", () => {
  const ctx = { mode: "self" as const };
  it("sends only the changed field", () => {
    const u = user();
    const result = planEdit(u, { ...editFormOf(u), displayName: "Alice B" }, ctx);
    expect(result.ok && result.plan.patch).toEqual({ displayName: "Alice B" });
  });
  it("reports nothing to do when nothing changed", () => {
    const u = user();
    const result = planEdit(u, editFormOf(u), ctx);
    expect(result.ok && result.empty).toBe(true);
  });
  it("treats blank timezone and locale as defaults", () => {
    const u = user({ timezone: "", locale: "" });
    const result = planEdit(u, { ...editFormOf(u), timezone: " ", locale: "" }, ctx);
    expect(result.ok && result.empty).toBe(true);
  });
  it("keeps an unknown role or status unless a known one is picked", () => {
    const u = user({ role: "auditor", status: "weird" });
    const same = planEdit(u, editFormOf(u), ctx);
    expect(same.ok && same.empty).toBe(true);
    const changed = planEdit(u, { ...editFormOf(u), role: "admin", status: "locked" }, ctx);
    expect(changed.ok && changed.plan.patch).toEqual({ role: "admin", status: "locked" });
  });
  it("sends the reason only with a status change", () => {
    const u = user();
    const a = planEdit(u, { ...editFormOf(u), reason: "why" }, ctx);
    expect(a.ok && a.plan.patch.reason).toBeUndefined();
    const b = planEdit(u, { ...editFormOf(u), status: "suspended", reason: "why" }, ctx);
    expect(b.ok && b.plan.patch).toEqual({ status: "suspended", reason: "why" });
  });
  it("hides balance in self mode and compares at six decimals otherwise", () => {
    const u = user();
    const self = planEdit(u, { ...editFormOf(u), balance: "99" }, ctx);
    expect(self.ok && self.plan.balance).toBeUndefined();
    const same = planEdit(u, { ...editFormOf(u), balance: "1.5000001" }, { mode: "usage" });
    expect(same.ok && same.plan.balance).toBeUndefined();
    const diff = planEdit(u, { ...editFormOf(u), balance: "2.25" }, { mode: "usage" });
    expect(diff.ok && diff.plan.balance).toBe(2.25);
    expect(planEdit(u, { ...editFormOf(u), balance: "-1" }, { mode: "usage" }).ok).toBe(false);
  });
  it("needs a tier in period mode and an expiry for paid ones", () => {
    const u = user();
    const period = { mode: "period" as const };
    expect(planEdit(u, { ...editFormOf(u), tier: "" }, period).ok).toBe(false);
    const paid = planEdit(u, { ...editFormOf(u), tier: "pro", expiryDate: "" }, period);
    expect(!paid.ok && paid.problem.key).toBe("adminUsers.problems.expiryRequired");
    const ok = planEdit(u, { ...editFormOf(u), tier: "pro", expiryDate: "2031-05-05", balance: "1.5" }, period);
    expect(ok.ok && ok.plan.patch.subscriptionTier).toBe("pro");
  });
  it("sends no subscription fields when neither tier nor expiry changed", () => {
    const u = user({ subscriptionTier: "pro", subscriptionExpiresAt: new Date(2031, 4, 5, 23, 59, 59).toISOString() });
    const period = { mode: "period" as const };
    const result = planEdit(u, { ...editFormOf(u) }, period);
    expect(result.ok && result.plan.patch.subscriptionTier).toBeUndefined();
  });
  it("needs a display name of 3 to 16 characters", () => {
    const u = user();
    expect(planEdit(u, { ...editFormOf(u), displayName: "ab" }, ctx).ok).toBe(false);
  });
});

describe("privileges", () => {
  it("lets only a superadmin manage superadmins", () => {
    expect(canManage("admin", { role: "superadmin" })).toBe(false);
    expect(canManage("superadmin", { role: "superadmin" })).toBe(true);
    expect(canManage("admin", { role: "user" })).toBe(true);
  });
  it("omits superadmin from the picker of a plain admin", () => {
    expect(roleChoices("admin", "user")).toEqual(["user", "admin"]);
    expect(roleChoices("superadmin", "user")).toEqual(["user", "admin", "superadmin"]);
    expect(roleChoices("admin", "auditor")).toEqual(["user", "admin", "auditor"]);
  });
});

describe("list helpers", () => {
  const list = [user({ id: 1, displayName: "Zed", role: "admin", lastLoginAt: "2026-03-01T00:00:00Z" }), user({ id: 2, displayName: "", username: "amy", subscriptionTier: "pro" }), user({ id: 3, displayName: "Bob", lastActiveAt: "2026-04-01T00:00:00Z" })];
  it("filters by role, status and tier", () => {
    expect(filterUsers(list, { role: "admin", status: "", tier: "" }).map((u) => u.id)).toEqual([1]);
    expect(filterUsers(list, { role: "", status: "", tier: "pro" }).map((u) => u.id)).toEqual([2]);
    expect(filterUsers(list, { role: "", status: "locked", tier: "" })).toEqual([]);
  });
  it("sorts by id, last activity and name", () => {
    expect(sortUsers(list, "id_desc").map((u) => u.id)).toEqual([3, 2, 1]);
    expect(sortUsers(list, "id_asc").map((u) => u.id)).toEqual([1, 2, 3]);
    expect(sortUsers(list, "last_login_desc").map((u) => u.id)).toEqual([3, 1, 2]);
    expect(sortUsers(list, "display_name_asc").map((u) => u.id)).toEqual([2, 3, 1]);
  });
  it("adds built-in tiers the plans do not cover", () => {
    expect(tierChoices(["pro", "team"])).toEqual(["pro", "team", "free", "max", "ultra"]);
  });
  it("parses balances and shows the stored day", () => {
    expect(parseBalance("1.23456789")).toEqual({ ok: true, value: 1.234568 });
    expect(parseBalance("abc").ok).toBe(false);
    expect(parseBalance("").ok).toBe(false);
    expect(dayOf(null)).toBe("");
  });
});
