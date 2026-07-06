/** Pure rules of the accounts screen. */
import { endOfDayIso } from "../lib/format";
import { problem, type Problem } from "../lib/problem";
import type { BillingMode, UserRecord } from "../lib/wire";

export const ROLES = ["user", "admin", "superadmin"] as const;
export const STATUSES = ["pending_activation", "active", "locked", "suspended", "deactivated"] as const;
export const SETTABLE_STATUSES = ["active", "locked", "suspended", "deactivated"] as const;
export const SUBSCRIPTION_STATUSES = ["active", "trialing", "past_due", "canceled", "unpaid", "incomplete", "incomplete_expired", "paused"] as const;
export const BUILTIN_TIERS = ["free", "pro", "max", "ultra"] as const;

export const RESERVED_USERNAMES = ["admin", "api", "auth", "billing", "chat", "riochat", "files", "help", "me", "root", "settings", "support", "system", "user", "users"] as const;

const chars = (s: string) => [...s].length;

export function validateUsername(raw: string): Problem | null {
  const name = raw.trim().toLowerCase();
  if (chars(name) < 3 || chars(name) > 16) return problem("adminUsers.problems.usernameLength", { min: 3, max: 16 });
  if (name.includes("@") || !/^[a-z0-9](?:[a-z0-9_-]*[a-z0-9])?$/.test(name)) return problem("adminUsers.problems.usernameShape");
  if ((RESERVED_USERNAMES as readonly string[]).includes(name)) return problem("adminUsers.problems.usernameReserved");
  return null;
}

/** Blank is allowed; otherwise 3 to 16 characters counted by code points. */
export function validateDisplayName(raw: string, required = false): Problem | null {
  const name = raw.trim();
  if (!name) return required ? problem("adminUsers.problems.displayNameLength", { min: 3, max: 16 }) : null;
  return chars(name) < 3 || chars(name) > 16 ? problem("adminUsers.problems.displayNameLength", { min: 3, max: 16 }) : null;
}

export function validatePassword(raw: string): Problem | null {
  const pw = raw.trim();
  if (chars(pw) < 8 || chars(pw) > 128) return problem("adminUsers.problems.passwordLength", { min: 8, max: 128 });
  if (/^\d+$/.test(pw)) return problem("adminUsers.problems.passwordDigits");
  return null;
}

export interface CreateForm {
  avatarURL: string;
  username: string;
  displayName: string;
  password: string;
  email: string;
  timezone: string;
  locale: string;
  tier: string;
  expiryDate: string;
}

export interface CreateBody {
  avatarURL: string;
  username: string;
  password: string;
  displayName: string;
  email: string;
  timezone: string;
  locale: string;
  subscriptionTier?: string;
  subscriptionExpiresAt?: string;
}

/** The first plan code, otherwise `free`. */
export function defaultTier(planCodes: readonly string[]): string {
  return planCodes[0] ?? "free";
}

export function validateCreate(form: CreateForm, mode: BillingMode): Problem | null {
  const checks = [validateUsername(form.username), validateDisplayName(form.displayName), validatePassword(form.password)];
  for (const found of checks) if (found) return found;
  if (mode === "period" && form.tier && form.tier !== "free") {
    if (!form.expiryDate.trim()) return problem("adminUsers.problems.expiryRequired");
    if (!endOfDayIso(form.expiryDate)) return problem("adminUsers.problems.expiryInvalid");
  }
  return null;
}

export function createBody(form: CreateForm, mode: BillingMode): CreateBody {
  const body: CreateBody = {
    avatarURL: form.avatarURL,
    username: form.username.trim().toLowerCase(),
    password: form.password.trim(),
    displayName: form.displayName.trim(),
    email: form.email.trim(),
    timezone: form.timezone || "Etc/UTC",
    locale: form.locale || "en-US",
  };
  if (mode === "period") {
    body.subscriptionTier = form.tier || "free";
    if (body.subscriptionTier !== "free") {
      const expiry = endOfDayIso(form.expiryDate);
      if (expiry) body.subscriptionExpiresAt = expiry;
    }
  }
  return body;
}

// --- edit ---------------------------------------------------------------------

export interface EditForm {
  avatarURL: string;
  displayName: string;
  email: string;
  phone: string;
  role: string;
  status: string;
  timezone: string;
  locale: string;
  profilePreferences: string;
  tier: string;
  expiryDate: string;
  balance: string;
  reason: string;
}

/** Day shown in the expiry input: the local calendar day of the stored instant. */
export function dayOf(iso: string | null | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

export function editFormOf(user: UserRecord): EditForm {
  return {
    avatarURL: user.avatarURL,
    displayName: user.displayName,
    email: user.email,
    phone: user.phone,
    role: user.role,
    status: user.status,
    timezone: user.timezone,
    locale: user.locale,
    profilePreferences: user.profilePreferences,
    tier: user.subscriptionTier || "free",
    expiryDate: dayOf(user.subscriptionExpiresAt),
    balance: String(user.billingBalanceUSD ?? 0),
    reason: "",
  };
}

/** Balances compare at six decimals. */
export function roundBalance(value: number): number {
  return Math.round(value * 1e6) / 1e6;
}

export type BalanceParse = { ok: true; value: number } | { ok: false };

/** A finite, non-negative number, rounded to six decimals. */
export function parseBalance(text: string): BalanceParse {
  const source = text.trim();
  if (source === "") return { ok: false };
  const n = Number(source);
  if (!Number.isFinite(n) || n < 0) return { ok: false };
  return { ok: true, value: roundBalance(n) };
}

export interface EditPatch {
  avatarURL?: string;
  displayName?: string;
  email?: string;
  phone?: string;
  role?: string;
  status?: string;
  timezone?: string;
  locale?: string;
  profilePreferences?: string;
  subscriptionTier?: string;
  subscriptionExpiresAt?: string;
  reason?: string;
}

export interface EditPlan {
  patch: EditPatch;
  /** New balance in USD when it changed. */
  balance?: number;
}

export interface EditContext {
  mode: BillingMode;
}

export type EditResult = { ok: true; plan: EditPlan; empty: boolean } | { ok: false; problem: Problem };

/**
 * Only changed fields travel. Role and status are sent only when they are
 * known values (an unknown one on the account is preserved). Blank timezone
 * and locale mean the defaults. The reason accompanies a status change only.
 */
export function planEdit(user: UserRecord, form: EditForm, ctx: EditContext): EditResult {
  const patch: EditPatch = {};
  const nameProblem = validateDisplayName(form.displayName, true);
  if (nameProblem) return { ok: false, problem: nameProblem };
  const set = <K extends keyof EditPatch>(key: K, value: EditPatch[K], original: unknown) => {
    if (value !== original) patch[key] = value;
  };
  set("avatarURL", form.avatarURL, user.avatarURL);
  set("displayName", form.displayName.trim(), user.displayName);
  set("email", form.email.trim(), user.email);
  set("phone", form.phone.trim(), user.phone);
  if ((ROLES as readonly string[]).includes(form.role)) set("role", form.role, user.role);
  if ((STATUSES as readonly string[]).includes(form.status) && form.status !== user.status) patch.status = form.status;
  set("timezone", form.timezone.trim() || "Etc/UTC", user.timezone || "Etc/UTC");
  set("locale", form.locale.trim() || "en-US", user.locale || "en-US");
  set("profilePreferences", form.profilePreferences, user.profilePreferences);

  if (ctx.mode === "period") {
    const tier = form.tier.trim();
    if (!tier) return { ok: false, problem: problem("adminUsers.problems.tierRequired") };
    const expiry = tier === "free" ? undefined : endOfDayIso(form.expiryDate);
    if (tier !== "free") {
      if (!form.expiryDate.trim()) return { ok: false, problem: problem("adminUsers.problems.expiryRequired") };
      if (!expiry) return { ok: false, problem: problem("adminUsers.problems.expiryInvalid") };
    }
    const tierChanged = tier !== (user.subscriptionTier || "free");
    const expiryChanged = tier !== "free" && form.expiryDate !== dayOf(user.subscriptionExpiresAt);
    if (tierChanged || expiryChanged) {
      patch.subscriptionTier = tier;
      if (expiry) patch.subscriptionExpiresAt = expiry;
    }
  }
  if (patch.status !== undefined && form.reason.trim()) patch.reason = form.reason.trim();

  let balance: number | undefined;
  if (ctx.mode !== "self") {
    const parsed = parseBalance(form.balance);
    if (!parsed.ok) return { ok: false, problem: problem("adminUsers.problems.balanceInvalid") };
    if (parsed.value !== roundBalance(user.billingBalanceUSD ?? 0)) balance = parsed.value;
  }
  const plan: EditPlan = balance === undefined ? { patch } : { patch, balance };
  return { ok: true, plan, empty: Object.keys(patch).length === 0 && balance === undefined };
}

// --- privilege ---------------------------------------------------------------------

/** A plain admin cannot manage a superadmin. */
export function canManage(viewerRole: string | undefined, target: Pick<UserRecord, "role">): boolean {
  return target.role !== "superadmin" || viewerRole === "superadmin";
}

export function roleChoices(viewerRole: string | undefined, current: string): string[] {
  const base = viewerRole === "superadmin" ? [...ROLES] : ROLES.filter((r) => r !== "superadmin");
  return base.includes(current as (typeof ROLES)[number]) || !current ? [...base] : [...base, current];
}

// --- list: client-side filter and sort -------------------------------------------------

export const USER_SORTS = ["id_desc", "id_asc", "last_login_desc", "updated_desc", "display_name_asc"] as const;
export type UserSort = (typeof USER_SORTS)[number];

export interface UserFilters {
  role: string;
  status: string;
  tier: string;
}

export function filterUsers(users: readonly UserRecord[], f: UserFilters): UserRecord[] {
  return users.filter((u) => (!f.role || u.role === f.role) && (!f.status || u.status === f.status) && (!f.tier || (u.subscriptionTier || "free") === f.tier));
}

const stamp = (iso: string | null | undefined) => (iso ? Date.parse(iso) || 0 : 0);
const nameOf = (u: UserRecord) => u.displayName || u.username;

export function sortUsers(users: readonly UserRecord[], sort: UserSort): UserRecord[] {
  const list = [...users];
  switch (sort) {
    case "id_asc":
      return list.sort((a, b) => a.id - b.id);
    case "last_login_desc":
      return list.sort((a, b) => stamp(b.lastActiveAt || b.lastLoginAt) - stamp(a.lastActiveAt || a.lastLoginAt) || b.id - a.id);
    case "updated_desc":
      return list.sort((a, b) => stamp(b.updatedAt) - stamp(a.updatedAt) || b.id - a.id);
    case "display_name_asc":
      return list.sort((a, b) => nameOf(a).localeCompare(nameOf(b), "en") || a.id - b.id);
    default:
      return list.sort((a, b) => b.id - a.id);
  }
}

/** Tier choices: the plans by code plus the built-in tiers they do not already cover. */
export function tierChoices(planCodes: readonly string[]): string[] {
  return [...planCodes, ...BUILTIN_TIERS.filter((t) => !planCodes.includes(t))];
}

export function bulkReason(kind: "role" | "status"): string {
  return kind === "role" ? "bulk_update_role" : "bulk_update_status";
}
