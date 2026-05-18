import type { FormatArgs } from "@/lib/i18n/format";
import type { MessageKey, Translate } from "@/lib/i18n/provider";

/**
 * A validation failure described as data: a catalog key plus arguments. Pure
 * validators return these so they can be tested without a translator, and the
 * screens render them with `problemText`.
 */
export interface Problem {
  key: MessageKey;
  args?: FormatArgs;
}

export function problem(key: MessageKey, args?: FormatArgs): Problem {
  return args ? { key, args } : { key };
}

/**
 * Renders a problem. An argument named `field` that holds a catalog key (it
 * starts with "admin") is translated first, so messages can name the field in
 * the viewer's language.
 */
export function problemText(t: Translate, p: Problem): string {
  const args: FormatArgs = { ...p.args };
  const field = args.field;
  if (typeof field === "string" && /^admin[A-Z]\w*\./.test(field)) args.field = t(field as MessageKey);
  return t(p.key, args);
}
