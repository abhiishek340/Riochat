/** Global shortcuts of the shell, decided without touching the DOM so they can be tested. */

export type ShortcutAction = "search" | "new-chat";

export interface KeyLike {
  key: string;
  metaKey: boolean;
  ctrlKey: boolean;
  shiftKey: boolean;
  altKey: boolean;
  isComposing?: boolean;
}

export function isApplePlatform(platform: string): boolean {
  return /mac|iphone|ipad|ipod/i.test(platform);
}

/** Cmd on Apple platforms, Ctrl elsewhere: K opens search, Shift+O starts a chat. Ignored while an IME composes. */
export function matchShortcut(event: KeyLike, apple: boolean): ShortcutAction | null {
  if (event.isComposing || event.altKey) return null;
  const modifier = apple ? event.metaKey && !event.ctrlKey : event.ctrlKey && !event.metaKey;
  if (!modifier) return null;
  const key = event.key.toLowerCase();
  if (key === "k" && !event.shiftKey) return "search";
  if (key === "o" && event.shiftKey) return "new-chat";
  return null;
}

export function modifierLabel(apple: boolean): string {
  return apple ? "⌘" : "Ctrl";
}

export const OPEN_SEARCH_EVENT = "riochat:search-open";
export const NEW_CHAT_EVENT = "riochat:new-chat";

export function requestOpenSearch(): void {
  if (typeof window !== "undefined") window.dispatchEvent(new Event(OPEN_SEARCH_EVENT));
}
