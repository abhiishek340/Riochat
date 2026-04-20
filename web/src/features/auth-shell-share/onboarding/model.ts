/** Decisions of the first-run wizard that need no React: validation, step order, stored preferences. */
import type { AuthUser } from "@/lib/api/types";
import { isRecord } from "@/lib/utils/guards";
import { ACCENTS, DEFAULT_ACCENT, type Accent } from "@/lib/theme/config";
import { ACCOUNT_PRESET_OF, accentFromAccountPreset } from "@/lib/theme/account-preset";
import { DISPLAY_NAME_MAX, isValidDisplayName, isValidPassword, isValidUsername, normalizeUsername } from "../lib/password-policy";

export const STEPS = ["welcome", "account", "twoFactor", "theme", "personalize", "ready"] as const;
export type StepName = (typeof STEPS)[number];

export const LAST_STEP = STEPS.length;

/** Previous step; the two-factor step deliberately has no way back (a started setup would be orphaned). */
export function previousStep(step: number): number | null {
  if (step === 3 || step <= 1) return null;
  return step - 1;
}

export function isBootstrap(user: Pick<AuthUser, "mustResetPassword">): boolean {
  return user.mustResetPassword === true;
}

export function isAdminUser(user: Pick<AuthUser, "role">): boolean {
  return user.role === "admin" || user.role === "superadmin";
}

export type AccountProblem = "changeUsername" | "usernameShape" | "displayName" | "password";

export interface AccountDraft {
  username: string;
  displayName: string;
  password: string;
}

/** The first problem with the account step, or null when it may be saved. */
export function validateAccount(user: Pick<AuthUser, "username" | "mustResetPassword" | "initialUsernameRequired">, draft: AccountDraft): AccountProblem | null {
  const bootstrap = isBootstrap(user);
  if (user.initialUsernameRequired) {
    if (normalizeUsername(draft.username) === normalizeUsername(user.username)) return "changeUsername";
    if (!isValidUsername(draft.username)) return "usernameShape";
  }
  if (!bootstrap && !isValidDisplayName(draft.displayName)) return "displayName";
  if (bootstrap && !isValidPassword(draft.password)) return "password";
  return null;
}

export interface AccountSavePlan {
  username: string | null;
  displayName: string | null;
}

/** Which profile calls the account step needs. The bootstrap password is never sent here. */
export function planAccountSave(user: Pick<AuthUser, "mustResetPassword" | "initialUsernameRequired" | "displayName">, draft: AccountDraft): AccountSavePlan {
  const bootstrap = isBootstrap(user);
  const trimmedName = draft.displayName.trim();
  return {
    username: user.initialUsernameRequired ? normalizeUsername(draft.username) : null,
    displayName: !bootstrap && trimmedName !== user.displayName ? trimmedName : null,
  };
}

export const APPEARANCE_DEFAULTS = { chatFont: "default", chatFontWeight: "regular", fontSize: "standard" } as const;

/** The account-stored appearance bundle with `preset` replaced; the other keys are kept (or defaulted). */
export function appearanceWithPreset(raw: string, preset: Accent): string {
  let current: Record<string, unknown> = {};
  try {
    const parsed: unknown = JSON.parse(raw);
    if (isRecord(parsed)) current = parsed;
  } catch {
    /* an empty or damaged value starts from defaults */
  }
  return JSON.stringify({
    preset: ACCOUNT_PRESET_OF[preset],
    chatFont: typeof current.chatFont === "string" ? current.chatFont : APPEARANCE_DEFAULTS.chatFont,
    chatFontWeight: typeof current.chatFontWeight === "string" ? current.chatFontWeight : APPEARANCE_DEFAULTS.chatFontWeight,
    fontSize: typeof current.fontSize === "string" ? current.fontSize : APPEARANCE_DEFAULTS.fontSize,
  });
}

export function storedPreset(raw: string): Accent {
  try {
    const parsed: unknown = JSON.parse(raw);
    const accent = isRecord(parsed) ? accentFromAccountPreset(parsed.preset) : undefined;
    if (accent) return accent;
  } catch {
    /* fall through */
  }
  return DEFAULT_ACCENT;
}

export const PRESET_CHOICES: readonly Accent[] = ACCENTS;

export function detectTimezone(): string {
  try {
    return Intl.DateTimeFormat().resolvedOptions().timeZone || "Etc/UTC";
  } catch {
    return "Etc/UTC";
  }
}

const FALLBACK_ZONES = [
  "Etc/UTC",
  "America/Los_Angeles",
  "America/New_York",
  "America/Sao_Paulo",
  "Europe/London",
  "Europe/Berlin",
  "Africa/Cairo",
  "Asia/Dubai",
  "Asia/Kolkata",
  "Asia/Shanghai",
  "Asia/Tokyo",
  "Australia/Sydney",
];

export function timezoneChoices(current: string): string[] {
  let zones: string[] = [];
  try {
    const fn = (Intl as unknown as { supportedValuesOf?: (key: string) => string[] }).supportedValuesOf;
    zones = fn ? fn("timeZone") : [];
  } catch {
    zones = [];
  }
  const list = zones.length ? zones : FALLBACK_ZONES;
  return list.includes(current) || !current ? list : [current, ...list];
}

/** First sign-in (no timezone on file yet) starts from the device; otherwise from the profile. */
export function initialTimezone(profile: string, device: string): string {
  const stored = profile.trim();
  return stored && stored !== "Etc/UTC" ? stored : device;
}

export interface PersonalizePatch {
  timezone?: string;
}

export function personalizePatch(profileTimezone: string, chosen: string): PersonalizePatch {
  const baseline = profileTimezone.trim() || "Etc/UTC";
  return chosen !== baseline ? { timezone: chosen } : {};
}

export { DISPLAY_NAME_MAX };
