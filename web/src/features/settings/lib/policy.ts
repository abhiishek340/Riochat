/**
 * Input rules shared by several settings forms. Everything here is pure so the
 * rules can be tested without rendering anything.
 */

/** Length in Unicode code points (not UTF-16 units). */
export function codePointLength(text: string): number {
  let n = 0;
  for (const _ of text) n++;
  return n;
}

export const USERNAME_MIN = 3;
export const USERNAME_MAX = 16;
export const DISPLAY_NAME_MIN = 3;
export const DISPLAY_NAME_MAX = 16;
export const PROFILE_PREFERENCES_MAX = 1024;
export const PASSWORD_MIN = 8;
export const PASSWORD_MAX = 128;

const RESERVED_USERNAMES: ReadonlySet<string> = new Set([
  "admin",
  "api",
  "auth",
  "billing",
  "chat",
  "riochat",
  "files",
  "help",
  "me",
  "root",
  "settings",
  "support",
  "system",
  "user",
  "users",
]);

const USERNAME_SHAPE = /^[a-z0-9](?:[a-z0-9_-]*[a-z0-9])?$/;

export function normalizeUsername(raw: string): string {
  return raw.trim().toLowerCase();
}

/** True when the (already normalised) username satisfies the account rules. */
export function isValidUsername(raw: string): boolean {
  const name = normalizeUsername(raw);
  const len = codePointLength(name);
  if (len < USERNAME_MIN || len > USERNAME_MAX) return false;
  if (name.includes("@")) return false;
  if (RESERVED_USERNAMES.has(name)) return false;
  return USERNAME_SHAPE.test(name);
}

export function isValidDisplayName(raw: string): boolean {
  const len = codePointLength(raw.trim());
  return len >= DISPLAY_NAME_MIN && len <= DISPLAY_NAME_MAX;
}

/** 8 to 128 code points after trimming, and not made only of digits. */
export function isValidPassword(raw: string): boolean {
  const value = raw.trim();
  const len = codePointLength(value);
  if (len < PASSWORD_MIN || len > PASSWORD_MAX) return false;
  return !/^\d+$/.test(value);
}

/** Keeps the leading `max` code points. */
export function clipCodePoints(text: string, max: number): string {
  if (codePointLength(text) <= max) return text;
  return Array.from(text).slice(0, max).join("");
}

export type VerificationMethod = "none" | "two_factor" | "email";

/**
 * Methods available for a sensitive action: the authenticator when the user
 * has it, the mailbox when verification mail is on and the address is
 * verified; otherwise the single value `none`.
 */
export function availableVerificationMethods(input: {
  twoFactorEnabled: boolean;
  emailVerificationEnabled: boolean;
  emailVerified: boolean;
}): VerificationMethod[] {
  const out: VerificationMethod[] = [];
  if (input.twoFactorEnabled) out.push("two_factor");
  if (input.emailVerificationEnabled && input.emailVerified) out.push("email");
  return out.length ? out : ["none"];
}

/** Sanitises what the user types into a code box for the given method. */
export function sanitizeCode(method: VerificationMethod, raw: string): string {
  if (method === "email") return raw.replace(/\D/g, "").slice(0, 6);
  if (method === "two_factor") return raw.replace(/[^A-Za-z0-9-]/g, "").slice(0, 32);
  return "";
}

export function isCodeReady(method: VerificationMethod, code: string): boolean {
  if (method === "none") return true;
  if (method === "email") return /^\d{6}$/.test(code);
  return code.length >= 6;
}

/** Email bootstrap: no address yet, or an unverified address taken from a provider that was never bootstrapped. */
export function isEmailBootstrap(user: {
  email: string;
  emailSource: string;
  emailVerified: boolean;
  bootstrapUsed: boolean;
}): boolean {
  if (!user.email) return true;
  return user.emailSource === "provider_unverified" && !user.emailVerified && !user.bootstrapUsed;
}

/** Seconds left of a cooldown that ends at `until` (ms epoch). */
export function secondsLeft(until: number, now: number): number {
  return Math.max(0, Math.ceil((until - now) / 1000));
}
