/** Context window handling for the model form and the bulk edit. */
import type { OfficialItem } from "../billing/official";
import { isPlainObject } from "../lib/json";

export const MIN_WINDOW = 4096;
export const MAX_WINDOW = 16_000_000;
export const WINDOW_PRESETS = [64_000, 128_000, 256_000, 500_000, 1_000_000] as const;
export const AUTO_MARKER = "_riochatContextWindowMode";
export const WINDOW_ALIASES = ["context_window", "contextWindowTokens", "context_window_tokens"] as const;

export function validWindow(n: number): boolean {
  return Number.isInteger(n) && n >= MIN_WINDOW && n <= MAX_WINDOW;
}

export type WindowInput = { kind: "auto" } | { kind: "value"; value: number } | { kind: "invalid" };

/** Digits only (commas and spaces are dropped); empty means automatic; out of range is invalid. */
export function parseWindowInput(text: string): WindowInput {
  const digits = text.replace(/[\s,_]/g, "");
  if (digits === "") return { kind: "auto" };
  if (!/^\d+$/.test(digits)) return { kind: "invalid" };
  const n = Number(digits);
  return validWindow(n) ? { kind: "value", value: n } : { kind: "invalid" };
}

/** `128000` as `128,000`. */
export function groupDigits(n: number | string): string {
  return String(n).replace(/\D/g, "").replace(/\B(?=(\d{3})+(?!\d))/g, ",");
}

type Caps = Record<string, unknown>;

export function windowOf(caps: Caps): { mode: "auto" | "custom" | "none"; value: number | null } {
  const raw = caps.contextWindow;
  const value = typeof raw === "number" && Number.isFinite(raw) ? raw : null;
  if (value === null) return { mode: "none", value: null };
  return { mode: String(caps[AUTO_MARKER] ?? "").toLowerCase() === "auto" ? "auto" : "custom", value };
}

function clearWindow(caps: Caps): Caps {
  const out = { ...caps };
  delete out.contextWindow;
  delete out[AUTO_MARKER];
  for (const alias of WINDOW_ALIASES) delete out[alias];
  return out;
}

/** A custom value is written without the marker; the aliases and the marker go. */
export function writeCustomWindow(caps: Caps, value: number): Caps {
  return { ...clearWindow(caps), contextWindow: value };
}

/** The detected value is written with the automatic marker. */
export function writeAutoWindow(caps: Caps, detected: number): Caps {
  return { ...clearWindow(caps), contextWindow: detected, [AUTO_MARKER]: "auto" };
}

export function removeWindow(caps: Caps): Caps {
  return clearWindow(caps);
}

export const nfkc = (text: string) => text.normalize("NFKC").trim().toLowerCase();

export interface AutoWindowInput {
  catalog: number | null;
  openRouter: number | null;
  /** The automatic value already stored, if any. */
  previousAuto: number | null;
  /** Whether a catalog could be consulted at all. */
  catalogConsulted: boolean;
  /** The model name and vendor are the same as when the previous value was stored. */
  identityUnchanged: boolean;
}

/**
 * The window to store in automatic mode: the catalog, else OpenRouter. With
 * no catalog available the previous automatic value is kept only while the
 * identity is unchanged; otherwise nothing is stored and backend defaults apply.
 */
export function resolveAutoWindow(input: AutoWindowInput): number | null {
  if (input.catalog !== null && validWindow(input.catalog)) return input.catalog;
  if (input.openRouter !== null && validWindow(input.openRouter)) return input.openRouter;
  if (!input.catalogConsulted && input.previousAuto !== null && input.identityUnchanged) return input.previousAuto;
  return null;
}

export function sameIdentity(a: { name: string; vendor: string }, b: { name: string; vendor: string }): boolean {
  return nfkc(a.name) === nfkc(b.name) && nfkc(a.vendor) === nfkc(b.vendor);
}

// --- OpenRouter fallback -----------------------------------------------------------------

const VENDOR_PREFIX: Record<string, string[]> = {
  anthropic: ["anthropic"],
  google: ["google"],
  meta: ["meta-llama"],
  microsoft: ["microsoft"],
  amazon: ["amazon"],
  nvidia: ["nvidia"],
  deepseek: ["deepseek"],
  moonshot: ["moonshotai"],
  zhipu: ["thudm", "z-ai"],
  minimax: ["minimax"],
  bytedance: ["bytedance"],
  tencent: ["tencent"],
  openai: ["openai"],
  xai: ["x-ai"],
};

const squash = (text: string) => nfkc(text).replace(/[^a-z0-9]+/g, "");
const tail = (text: string) => text.slice(text.lastIndexOf("/") + 1);

function prefixOf(item: OfficialItem): string {
  const slash = item.id.indexOf("/");
  return slash > 0 ? item.id.slice(0, slash).toLowerCase() : "";
}

/**
 * Finds the OpenRouter entry for a model: exact id, slug or name signature
 * first, then the last path segment. Several candidates are narrowed by the
 * provider prefix in the name or by the vendor's known prefixes. A tie is not
 * guessed. Only a context length inside the valid range counts.
 */
export function openRouterWindow(name: string, vendor: string, items: readonly OfficialItem[]): number | null {
  const wanted = nfkc(name);
  if (!wanted) return null;
  const signature = squash(wanted);
  const exact = items.filter((i) => [i.id, i.canonicalSlug, i.name].some((s) => s && (nfkc(s) === wanted || squash(s) === signature)));
  let candidates = exact;
  if (candidates.length === 0) {
    const last = squash(tail(wanted));
    candidates = items.filter((i) => [i.id, i.canonicalSlug].some((s) => s && squash(tail(s)) === last) || squash(tail(i.name)) === last);
  }
  if (candidates.length > 1) {
    const prefixes = new Set<string>();
    const slash = wanted.indexOf("/");
    if (slash > 0) prefixes.add(wanted.slice(0, slash));
    for (const p of VENDOR_PREFIX[nfkc(vendor)] ?? []) prefixes.add(p);
    const narrowed = candidates.filter((i) => prefixes.has(prefixOf(i)));
    candidates = narrowed;
  }
  if (candidates.length !== 1) return null;
  const length = (candidates[0] as OfficialItem).contextLength;
  return typeof length === "number" && validWindow(length) ? length : null;
}

// --- bulk window edit ------------------------------------------------------------------------

export interface BulkWindowModel {
  platformModelName: string;
  capabilitiesJSON: string;
}

export function maxOutputOf(caps: Caps): number | null {
  for (const key of ["maxOutputTokens", "max_output_tokens"]) {
    const v = caps[key];
    if (typeof v === "number" && Number.isFinite(v)) return v;
  }
  return null;
}

export type BulkWindowProblem = { kind: "invalid" } | { kind: "maxOutput"; model: string } | { kind: "notObject"; model: string };

/**
 * A bulk window is blocked when the value is invalid, when a model's maximum
 * output is not below it, or when a model's capabilities text is not an object.
 */
export function bulkWindowProblem(models: readonly BulkWindowModel[], input: string): BulkWindowProblem | null {
  const parsed = parseWindowInput(input);
  if (parsed.kind !== "value") return { kind: "invalid" };
  for (const m of models) {
    const text = m.capabilitiesJSON.trim();
    let caps: unknown = {};
    if (text) {
      try {
        caps = JSON.parse(text);
      } catch {
        return { kind: "notObject", model: m.platformModelName };
      }
    }
    if (!isPlainObject(caps)) return { kind: "notObject", model: m.platformModelName };
    const max = maxOutputOf(caps);
    if (max !== null && max >= parsed.value) return { kind: "maxOutput", model: m.platformModelName };
  }
  return null;
}
