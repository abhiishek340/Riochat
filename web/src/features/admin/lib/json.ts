/** JSON helpers for the free-form JSON fields of the console. */

export function isPlainObject(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

export type ObjectParse = { ok: true; value: Record<string, unknown> } | { ok: false; reason: "syntax" | "not_object" };

/** Blank text counts as an empty object. */
export function parseJsonObject(text: string): ObjectParse {
  const source = text.trim();
  if (source === "") return { ok: true, value: {} };
  let parsed: unknown;
  try {
    parsed = JSON.parse(source);
  } catch {
    return { ok: false, reason: "syntax" };
  }
  return isPlainObject(parsed) ? { ok: true, value: parsed } : { ok: false, reason: "not_object" };
}

export function prettyJson(value: unknown): string {
  return JSON.stringify(value, null, 2);
}

/** Pretty-prints JSON text when it parses, otherwise returns it unchanged. */
export function tryPretty(text: string): string {
  try {
    return prettyJson(JSON.parse(text));
  } catch {
    return text;
  }
}

export function safeParse(text: string | null | undefined): unknown {
  if (!text) return undefined;
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}
