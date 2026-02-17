"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useBranding } from "@/lib/branding/provider";
import { enUS, type Catalog } from "./catalog/en-US";
import { DEFAULT_LOCALE, detectLocale, normalizeLocale, persistLocale, type Locale } from "./config";
import { formatMessage, type FormatArgs } from "./format";
import { flatten, type Paths } from "./keys";

export type MessageKey = Paths<Catalog>;
export type Translate = (key: MessageKey, args?: FormatArgs) => string;

type FlatCatalog = Record<string, string>;

const flatEn: FlatCatalog = flatten(enUS);
const loaded = new Map<Locale, FlatCatalog>([["en-US", flatEn]]);

async function loadCatalog(locale: Locale): Promise<FlatCatalog> {
  return loaded.get(locale) ?? flatEn;
}

interface I18nValue {
  locale: Locale;
  /** False until the initial locale's catalog is in place. */
  ready: boolean;
  t: Translate;
  /** Untyped lookup for keys computed at runtime (error codes); undefined when absent. */
  lookup: (key: string, args?: FormatArgs) => string | undefined;
  setLocale: (locale: string, options?: { persist?: boolean }) => Promise<void>;
}

const Ctx = createContext<I18nValue | null>(null);

export function I18nProvider({ children }: { children: ReactNode }) {
  const { branding } = useBranding();
  const brand = branding.title;
  const [state, setState] = useState<{ locale: Locale; messages: FlatCatalog; ready: boolean }>({
    locale: DEFAULT_LOCALE,
    messages: flatEn,
    ready: false,
  });
  const latest = useRef<Locale>(DEFAULT_LOCALE);

  const apply = useCallback(async (target: Locale, persist: boolean) => {
    const messages = await loadCatalog(target);
    // Swap messages, state and <html lang> together so nothing renders mixed.
    latest.current = target;
    document.documentElement.lang = target;
    setState({ locale: target, messages, ready: true });
    if (persist) persistLocale(target);
  }, []);

  useEffect(() => {
    // Detection (cookie, browser languages) is client-only: the static HTML is
    // always rendered in the default locale, so first paint stays hydration-safe.
    void apply(detectLocale(), false).catch(() => setState((s) => ({ ...s, ready: true })));
  }, [apply]);

  const setLocale = useCallback(
    async (next: string, options?: { persist?: boolean }) => {
      const target = normalizeLocale(next);
      const persist = options?.persist ?? true;
      if (target === latest.current) {
        if (persist) persistLocale(target);
        return;
      }
      await apply(target, persist);
    },
    [apply],
  );

  const value = useMemo<I18nValue>(() => {
    const { locale, messages, ready } = state;
    const lookup = (key: string, args?: FormatArgs): string | undefined => {
      const template = messages[key] ?? flatEn[key];
      return template === undefined ? undefined : formatMessage(template, { brand, ...args }, locale);
    };
    const t: Translate = (key, args) => lookup(key, args) ?? key;
    return { locale, ready, t, lookup, setLocale };
  }, [state, brand, setLocale]);

  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useI18n(): I18nValue {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useI18n must be used inside <I18nProvider>");
  return ctx;
}

export function useT(): Translate {
  return useI18n().t;
}
