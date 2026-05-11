"use client";

/**
 * Per-device appearance: storage keys, the live store behind the hooks, and the
 * document attributes other parts of the app style against.
 *
 *   html[data-font-size]         absent when "standard"
 *   html[data-chat-font]         absent when "default"
 *   html[data-chat-font-weight]  absent when "regular"
 */
import { useCallback, useEffect, useRef, useSyncExternalStore, type ReactNode } from "react";
import { api } from "@/lib/api/client";
import { sessionStore } from "@/lib/api/session-store";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import { useSession } from "@/lib/session/provider";
import { toast } from "@/components/ui/toast-store";
import { useTheme } from "@/lib/theme/provider";
import { readStorage, writeStorage } from "@/lib/utils/storage";
import {
  DEFAULT_CHAT_FONT,
  DEFAULT_CHAT_FONT_WEIGHT,
  DEFAULT_FONT_SIZE,
  parseAppearanceBundle,
  readChatFont,
  readChatFontWeight,
  readFontSize,
  resolveAppearance,
  type AppearanceBundle,
  type ChatFont,
  type ChatFontWeight,
  type FontSize,
  type FullAppearance,
} from "./appearance-prefs";
import { AppearanceSaver } from "./appearance-saver";

export const FONT_SIZE_KEY = "riochat:font-size";
export const CHAT_FONT_KEY = "riochat:chat-font";
export const CHAT_FONT_WEIGHT_KEY = "riochat:chat-font-weight";

const CHANGE_EVENT = "riochat:appearance-changed";
const WATCHED_KEYS = [FONT_SIZE_KEY, CHAT_FONT_KEY, CHAT_FONT_WEIGHT_KEY];

function subscribe(onChange: () => void): () => void {
  const onStorage = (event: StorageEvent) => {
    if (event.key === null || WATCHED_KEYS.includes(event.key)) {
      applyAppearanceAttributes();
      onChange();
    }
  };
  window.addEventListener(CHANGE_EVENT, onChange);
  window.addEventListener("storage", onStorage);
  return () => {
    window.removeEventListener(CHANGE_EVENT, onChange);
    window.removeEventListener("storage", onStorage);
  };
}

function notify(): void {
  if (typeof window !== "undefined") window.dispatchEvent(new Event(CHANGE_EVENT));
}

const currentFontSize = (): FontSize => readFontSize(readStorage(FONT_SIZE_KEY));
const currentChatFont = (): ChatFont => readChatFont(readStorage(CHAT_FONT_KEY));
const currentChatFontWeight = (): ChatFontWeight => readChatFontWeight(readStorage(CHAT_FONT_WEIGHT_KEY));

/** Reactive reads (server render and first paint return the defaults). */
export function useFontSize(): FontSize {
  return useSyncExternalStore(subscribe, currentFontSize, () => DEFAULT_FONT_SIZE);
}
export function useChatFont(): ChatFont {
  return useSyncExternalStore(subscribe, currentChatFont, () => DEFAULT_CHAT_FONT);
}
export function useChatFontWeight(): ChatFontWeight {
  return useSyncExternalStore(subscribe, currentChatFontWeight, () => DEFAULT_CHAT_FONT_WEIGHT);
}

function setAttribute(root: HTMLElement, name: string, value: string, defaultValue: string): void {
  if (value === defaultValue) root.removeAttribute(name);
  else root.setAttribute(name, value);
}

/** Mirrors the three stored values onto the document root. */
export function applyAppearanceAttributes(): void {
  if (typeof document === "undefined") return;
  const root = document.documentElement;
  setAttribute(root, "data-font-size", currentFontSize(), DEFAULT_FONT_SIZE);
  setAttribute(root, "data-chat-font", currentChatFont(), DEFAULT_CHAT_FONT);
  setAttribute(root, "data-chat-font-weight", currentChatFontWeight(), DEFAULT_CHAT_FONT_WEIGHT);
}

export function setFontSize(value: FontSize): void {
  writeStorage(FONT_SIZE_KEY, value);
  applyAppearanceAttributes();
  notify();
}
export function setChatFont(value: ChatFont): void {
  writeStorage(CHAT_FONT_KEY, value);
  applyAppearanceAttributes();
  notify();
}
export function setChatFontWeight(value: ChatFontWeight): void {
  writeStorage(CHAT_FONT_WEIGHT_KEY, value);
  applyAppearanceAttributes();
  notify();
}

/** Applies the stored font preferences to the page and keeps them applied. */
export function AppearanceProvider({ children }: { children: ReactNode }) {
  const size = useFontSize();
  const font = useChatFont();
  const weight = useChatFontWeight();
  useEffect(() => {
    applyAppearanceAttributes();
  }, [size, font, weight]);
  return <>{children}</>;
}

/**
 * Renders nothing. Once the signed-in profile is loaded, adopts the
 * appearance saved on the account on this device: account values win for the
 * keys they contain. Runs again only when the stored string changes.
 */
export function AppearanceSync(): null {
  const { user, status } = useSession();
  const { accent, setAccent } = useTheme();
  const applied = useRef<string | null>(null);
  const accentRef = useRef(accent);
  accentRef.current = accent;
  const setAccentRef = useRef(setAccent);
  setAccentRef.current = setAccent;

  const publicID = user?.publicID ?? null;
  const raw = typeof user?.appearancePreferences === "string" ? user.appearancePreferences : "";

  useEffect(() => {
    if (status !== "ready" || publicID === null) {
      if (status !== "loading") applied.current = null;
      return;
    }
    const marker = `${publicID}\u0000${raw}`;
    if (applied.current === marker) return;
    applied.current = marker;

    const device: FullAppearance = {
      preset: accentRef.current,
      chatFont: currentChatFont(),
      chatFontWeight: currentChatFontWeight(),
      fontSize: currentFontSize(),
    };
    const next = resolveAppearance(device, parseAppearanceBundle(raw));
    if (next.preset !== device.preset) setAccentRef.current(next.preset);
    if (next.chatFont !== device.chatFont) setChatFont(next.chatFont);
    if (next.chatFontWeight !== device.chatFontWeight) setChatFontWeight(next.chatFontWeight);
    if (next.fontSize !== device.fontSize) setFontSize(next.fontSize);
  }, [status, publicID, raw]);

  useEffect(() => {
    if (!user && status === "loading") applied.current = null;
  }, [user, status]);

  return null;
}

/**
 * Debounced writer of appearance changes to the account. Pending changes are
 * sent when the calling page goes away rather than being lost.
 */
export function useAppearanceSaver(): { schedule: (patch: AppearanceBundle) => void } {
  const t = useT();
  const errorMessage = useErrorMessage();
  const { accent } = useTheme();
  const accentRef = useRef(accent);
  accentRef.current = accent;
  const errorRef = useRef({ t, errorMessage });
  errorRef.current = { t, errorMessage };

  const saver = useRef<AppearanceSaver | null>(null);
  if (saver.current === null) {
    saver.current = new AppearanceSaver({
      readDevice: () => ({
        preset: accentRef.current,
        chatFont: currentChatFont(),
        chatFontWeight: currentChatFontWeight(),
        fontSize: currentFontSize(),
      }),
      canSend: () => sessionStore.token() !== "",
      send: (appearancePreferences) => api.patch("/me", { appearancePreferences }),
      onError: (error) =>
        toast.error(errorRef.current.t("settings.general.toasts.saveFailed"), {
          description: errorRef.current.errorMessage(error),
        }),
    });
  }

  useEffect(() => {
    const instance = saver.current;
    return () => {
      void instance?.flush();
    };
  }, []);

  const schedule = useCallback((patch: AppearanceBundle) => saver.current?.schedule(patch), []);
  return { schedule };
}
