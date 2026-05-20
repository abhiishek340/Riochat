/** Pure rules of the profile form: draft, dirty checks, validation, error mapping. */
import type { AuthUser } from "@/lib/api/types";
import { clipCodePoints, isValidDisplayName, isValidUsername, normalizeUsername, PROFILE_PREFERENCES_MAX } from "../lib/policy";
import { usernameChangedAt } from "../lib/user";

export interface ProfileFields {
  avatarURL: string;
  displayName: string;
  timezone: string;
  locale: string;
  profilePreferences: string;
}

export const DEFAULT_TIMEZONE = "Etc/UTC";
export const DEFAULT_LOCALE = "en-US";

export function fieldsFromUser(user: AuthUser): ProfileFields {
  return {
    avatarURL: (user.avatarURL ?? "").trim(),
    displayName: (user.displayName ?? "").trim(),
    timezone: (user.timezone ?? "").trim() || DEFAULT_TIMEZONE,
    locale: (user.locale ?? "").trim() || DEFAULT_LOCALE,
    profilePreferences: clipCodePoints((user.profilePreferences ?? "").trim(), PROFILE_PREFERENCES_MAX),
  };
}

/** Only an account that never changed its username may change it (once). */
export function canEditUsername(user: AuthUser | null): boolean {
  return user !== null && usernameChangedAt(user) === "";
}

export function profileFieldsChanged(draft: ProfileFields, saved: ProfileFields): boolean {
  return (
    draft.avatarURL !== saved.avatarURL ||
    draft.displayName !== saved.displayName ||
    draft.timezone !== saved.timezone ||
    draft.locale !== saved.locale ||
    draft.profilePreferences !== saved.profilePreferences
  );
}

/** The username to send, or null when no username edit is pending. */
export function pendingUsername(user: AuthUser | null, usernameDraft: string): string | null {
  if (!canEditUsername(user)) return null;
  const next = normalizeUsername(usernameDraft);
  if (next === "" || next === user?.username) return null;
  return next;
}

export type SaveBlock = "usernameInvalid" | "displayNameInvalid";

/** The first reason a save must not go out, in the order the form checks them. */
export function saveBlock(input: { pendingUsername: string | null; fieldsChanged: boolean; displayName: string }): SaveBlock | null {
  if (input.pendingUsername !== null && !isValidUsername(input.pendingUsername)) return "usernameInvalid";
  if (input.fieldsChanged && !isValidDisplayName(input.displayName)) return "displayNameInvalid";
  return null;
}

export type UsernameFailure = "invalid" | "alreadyChanged" | "taken" | "other";

/** Classifies a failed username change by status, code and message. */
export function classifyUsernameFailure(failure: { status?: number; errorCode?: string; message?: string }): UsernameFailure {
  const { status, errorCode = "", message = "" } = failure;
  if (errorCode === "user.username_change_used") return "alreadyChanged";
  if (errorCode === "user.username_already_exists") return "taken";
  if (status === 400) return "invalid";
  if (status === 409) return /already used/i.test(message) ? "alreadyChanged" : "taken";
  return "other";
}

/** The five fields sent together, trimmed the way the saved snapshot is. */
export function profilePatchBody(draft: ProfileFields): ProfileFields {
  return {
    avatarURL: draft.avatarURL.trim(),
    displayName: draft.displayName.trim(),
    timezone: draft.timezone.trim() || DEFAULT_TIMEZONE,
    locale: draft.locale.trim() || DEFAULT_LOCALE,
    profilePreferences: draft.profilePreferences.trim(),
  };
}
