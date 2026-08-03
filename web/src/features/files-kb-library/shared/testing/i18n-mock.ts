/** Test double for the i18n provider: real en-US messages, no React context needed. */
import { enUS } from "@/lib/i18n/catalog/en-US";
import { formatMessage, type FormatArgs } from "@/lib/i18n/format";
import { flatten } from "@/lib/i18n/keys";

const flat = flatten(enUS) as Record<string, string>;

export const lookup = (key: string, args?: FormatArgs): string | undefined =>
  flat[key] === undefined ? undefined : formatMessage(flat[key], { brand: "Rio", ...args }, "en-US");

export const t = (key: string, args?: FormatArgs): string => lookup(key, args) ?? key;

export function providerModule() {
  const value = { t, lookup, locale: "en-US", ready: true, setLocale: async () => undefined };
  return { useT: () => t, useI18n: () => value };
}
