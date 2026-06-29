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
