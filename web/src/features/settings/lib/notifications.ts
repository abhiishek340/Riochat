/** Response-completion notifications: the device switch and the display rules. */
import { isDesktop } from "@/lib/api/platform";
import { readStorage, writeStorage } from "@/lib/utils/storage";

export const COMPLETION_NOTIFICATIONS_KEY = "riochat:response-completion-notifications";
export const NOTIFICATION_BODY_LIMIT = 140;

export type NotificationPermissionState = NotificationPermission | "unsupported";

export function readCompletionNotificationsEnabled(): boolean {
  return readStorage(COMPLETION_NOTIFICATIONS_KEY) === "true";
}

export function writeCompletionNotificationsEnabled(enabled: boolean): void {
  writeStorage(COMPLETION_NOTIFICATIONS_KEY, enabled ? "true" : "false");
}

/** Desktop shell notifications or the browser API; false when neither exists. */
export function notificationsSupported(): boolean {
  if (isDesktop()) return true;
  return typeof Notification !== "undefined";
}

/** The desktop shell treats permission as granted. */
export function currentPermission(): NotificationPermissionState {
  if (isDesktop()) return "granted";
  if (typeof Notification === "undefined") return "unsupported";
  return Notification.permission;
}

export async function requestPermission(): Promise<NotificationPermissionState> {
  if (isDesktop()) return "granted";
  if (typeof Notification === "undefined") return "unsupported";
  if (Notification.permission !== "default") return Notification.permission;
  try {
    return await Notification.requestPermission();
  } catch {
    return Notification.permission;
  }
}

/** Collapses whitespace and cuts at the limit with an ellipsis; falls back when empty. */
export function completionNotificationBody(text: string, fallback: string): string {
  const flat = text.replace(/\s+/g, " ").trim();
  if (!flat) return fallback;
  const chars = Array.from(flat);
  if (chars.length <= NOTIFICATION_BODY_LIMIT) return flat;
  return `${chars.slice(0, NOTIFICATION_BODY_LIMIT - 1).join("").trimEnd()}…`;
}

/** Shown only when on, allowed, and the page is not both visible and focused. */
export function shouldShowCompletionNotification(input: {
  enabled: boolean;
  permission: NotificationPermissionState;
  visible: boolean;
  focused: boolean;
}): boolean {
  return input.enabled && input.permission === "granted" && !(input.visible && input.focused);
}
