/**
 * Client-side input rules for credentials and names. The server enforces its
 * own limits; these keep the common mistakes from costing a round trip.
 */

export const PASSWORD_MIN = 8;
export const PASSWORD_MAX = 128;
export const USERNAME_MIN = 3;
export const USERNAME_MAX = 16;
export const DISPLAY_NAME_MIN = 3;
export const DISPLAY_NAME_MAX = 16;
export const CODE_LENGTH = 6;
export const RECOVERY_CODE_MAX = 32;

/** Names the service keeps for itself. */
export const RESERVED_USERNAMES: ReadonlySet<string> = new Set([
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

/** Length in Unicode code points (what a person counts), not UTF-16 units. */
export function codePointLength(value: string): number {
  let n = 0;
  for (const _ of value) n += 1;
  return n;
}

/** 8-128 characters after trimming, and not made of digits only. */
export function isValidPassword(raw: string): boolean {
  const value = raw.trim();
  const length = codePointLength(value);
  if (length < PASSWORD_MIN || length > PASSWORD_MAX) return false;
  return !/^\d+$/.test(value);
}

export function normalizeUsername(raw: string): string {
  return raw.trim().toLowerCase();
}

const USERNAME_SHAPE = /^[a-z0-9](?:[a-z0-9_-]*[a-z0-9])?$/;

/** 3-16 chars, alphanumeric at both ends, `_` and `-` allowed inside, never reserved. */
export function isValidUsername(raw: string): boolean {
  const value = normalizeUsername(raw);
  const length = codePointLength(value);
  if (length < USERNAME_MIN || length > USERNAME_MAX) return false;
  if (value.includes("@") || !USERNAME_SHAPE.test(value)) return false;
  return !RESERVED_USERNAMES.has(value);
}

export function isValidDisplayName(raw: string): boolean {
  const length = codePointLength(raw.trim());
  return length >= DISPLAY_NAME_MIN && length <= DISPLAY_NAME_MAX;
}

/** Keeps digits only, truncated: for one-time email and authenticator codes. */
export function digitsOnly(raw: string, max = CODE_LENGTH): string {
  return raw.replace(/\D+/g, "").slice(0, max);
}

/** Authenticator codes and recovery codes: letters, digits and hyphens. */
export function sanitizeSecondFactorCode(raw: string): string {
  return raw.replace(/[^A-Za-z0-9-]+/g, "").slice(0, RECOVERY_CODE_MAX);
}
