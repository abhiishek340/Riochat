/** Browser notifications for finished replies (opt-in, only when the page is not in front). */
import { readStorage, writeStorage } from "@/lib/utils/storage";

export const NOTIFY_KEY = "riochat:response-completion-notifications";

export function notificationsOptedIn(): boolean {
  return readStorage(NOTIFY_KEY) === "true";
}

export function setNotificationsOptIn(on: boolean): void {
  writeStorage(NOTIFY_KEY, on ? "true" : "false");
}

export function notificationBody(text: string, fallback: string): string {
  const flat = text.replace(/\s+/g, " ").trim();
  if (!flat) return fallback;
  const chars = Array.from(flat);
  return chars.length > 140 ? `${chars.slice(0, 139).join("")}…` : flat;
}

export function pageInFront(): boolean {
  return typeof document !== "undefined" && document.visibilityState === "visible" && document.hasFocus();
}

export interface NotifyInput {
  title: string;
  body: string;
  tag: string;
  icon?: string;
}

/** Shows the notification when allowed; returns whether it did. */
export function notifyDone(input: NotifyInput): boolean {
  if (typeof Notification === "undefined") return false;
  if (!notificationsOptedIn() || Notification.permission !== "granted" || pageInFront()) return false;
  try {
    const n = new Notification(input.title, { body: input.body, tag: input.tag, icon: input.icon });
    n.onclick = () => {
      window.focus();
      n.close();
    };
    return true;
  } catch {
    return false;
  }
}
