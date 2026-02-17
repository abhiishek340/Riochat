import { writeCookie } from "../utils/storage";

export const LOCALES = ["en-US"] as const;
export type Locale = (typeof LOCALES)[number];
export const DEFAULT_LOCALE: Locale = "en-US";
export const LOCALE_COOKIE = "riochat_locale";
const ONE_YEAR = 60 * 60 * 24 * 365;

export const LOCALE_LABELS: Record<Locale, string> = {
  "en-US": "English",
};

export function isLocale(value: unknown): value is Locale {
  return typeof value === "string" && (LOCALES as readonly string[]).includes(value);
}

/** English is the only supported locale, so every input resolves to it. */
export function normalizeLocale(_input: string | null | undefined): Locale {
  return DEFAULT_LOCALE;
}

/** The default locale; kept as a function so a second locale can be added later. */
export function detectLocale(): Locale {
  return DEFAULT_LOCALE;
}

export function persistLocale(locale: Locale): void {
  writeCookie(LOCALE_COOKIE, locale, ONE_YEAR);
}
