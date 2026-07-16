/**
 * Administrator-declared model controls (reasoning effort, toggles, numeric
 * knobs): normalisation, validation of chosen values, stepper arithmetic and
 * toolbar placement.
 */
import type { ControlDTO } from "../types";

export type ControlValue = string | number;
export type ControlChoices = Record<string, ControlValue>;
export type Placement = "toolbar" | "menu";

export const MAX_TOOLBAR_PINS = 6;
export const REASONING_CONTROL_ID = "reasoning";

const SAMPLING = new Set(["temperature", "top_p", "min_p", "top_a", "frequency_penalty", "presence_penalty", "repetition_penalty"]);

/** Drops controls that cannot be shown: no options (unless numeric) or duplicate ids. */
export function normalizeControls(list: readonly ControlDTO[] | null | undefined): ControlDTO[] {
  const seen = new Set<string>();
  const out: ControlDTO[] = [];
  for (const c of list ?? []) {
    if (!c || typeof c.id !== "string" || !c.id || seen.has(c.id)) continue;
    const numeric = c.type === "number";
    const toggle = c.type === "toggle";
    if (!numeric && !toggle && (!c.options || c.options.length === 0)) continue;
    seen.add(c.id);
    out.push(c);
  }
  return out;
}

export function isNumericControl(c: ControlDTO): boolean {
  return c.type === "number";
}

/** Granularity of the +/- buttons. */
export function stepOf(c: ControlDTO): number {
  if (typeof c.step === "number" && c.step > 0) return c.step;
  if (c.integer) return 1;
  const fractionalDefault = c.default !== null && c.default !== undefined && c.default !== "" && !Number.isInteger(Number(c.default)) && Number.isFinite(Number(c.default));
  const small = typeof c.min === "number" && typeof c.max === "number" && c.max - c.min <= 2;
  if (SAMPLING.has(c.id) || small || fractionalDefault) return 0.1;
  return 1;
}

function decimals(n: number): number {
  const s = String(n);
  const e = /e-(\d+)$/.exec(s);
  if (e) return Number(e[1]);
  const i = s.indexOf(".");
  return i < 0 ? 0 : s.length - i - 1;
}

export function roundToStep(value: number, step: number): number {
  const d = Math.max(decimals(step), decimals(value));
  return Number(value.toFixed(Math.min(d, 10)));
}

/** Parses and validates a number for a control; null when invalid. */
export function validateNumber(c: ControlDTO, raw: unknown): number | null {
  const n = typeof raw === "number" ? raw : typeof raw === "string" && raw.trim() !== "" ? Number(raw) : NaN;
  if (!Number.isFinite(n)) return null;
  if (typeof c.min === "number" && n < c.min) return null;
  if (typeof c.max === "number" && n > c.max) return null;
  if (c.integer && !Number.isInteger(n)) return null;
  if (typeof c.step === "number" && c.step > 0) {
    const base = typeof c.min === "number" ? c.min : 0;
    const ratio = (n - base) / c.step;
    if (Math.abs(ratio - Math.round(ratio)) > 1e-9) return null;
  }
  return n;
}

/** The canonical value for a stored choice, or null when it does not fit the control. */
export function validateChoice(c: ControlDTO, raw: unknown): ControlValue | null {
  if (c.locked) return null;
  if (c.type === "number") return validateNumber(c, raw);
  if (c.type === "toggle") return raw === "on" || raw === "off" ? raw : null;
  if (typeof raw !== "string" && typeof raw !== "number") return null;
  const text = String(raw);
  return (c.options ?? []).some((o) => o.value === text) ? text : null;
}

/** Keeps only explicit, valid, unlocked choices for the given controls. */
export function pruneChoices(controls: readonly ControlDTO[], stored: unknown): ControlChoices {
  const out: ControlChoices = {};
  if (typeof stored !== "object" || stored === null || Array.isArray(stored)) return out;
  const map = stored as Record<string, unknown>;
  for (const c of controls) {
    if (!(c.id in map)) continue;
    const v = validateChoice(c, map[c.id]);
    if (v !== null) out[c.id] = v;
  }
  return out;
}

/** The `controls` field of a request: valid choices only, undefined when empty. */
export function requestControls(controls: readonly ControlDTO[], choices: ControlChoices): ControlChoices | undefined {
  const pruned = pruneChoices(controls, choices);
  return Object.keys(pruned).length > 0 ? pruned : undefined;
}

/** What a control currently shows: the choice, else the admin default. */
export function displayValue(c: ControlDTO, choices: ControlChoices): ControlValue | null {
  if (c.locked) return c.default ?? null;
  if (c.id in choices) return choices[c.id] as ControlValue;
  return c.default ?? null;
}

export function isChanged(c: ControlDTO, choices: ControlChoices): boolean {
  return !c.locked && c.id in choices && String(choices[c.id]) !== String(c.default ?? "");
}

/** Next numeric value after pressing + or -; null when the result leaves the range. */
export function stepValue(c: ControlDTO, current: number, direction: 1 | -1): number | null {
  const step = stepOf(c);
  const next = roundToStep(current + direction * step, step);
  if (typeof c.min === "number" && next < c.min) return null;
  if (typeof c.max === "number" && next > c.max) return null;
  return next;
}

// --- placement ----------------------------------------------------------------------

export interface PlaceableItem {
  id: string;
  defaultPlacement: Placement;
}

export type PlacementOverrides = Record<string, Placement>;

export function parsePlacementOverrides(json: string | null | undefined): PlacementOverrides {
  if (!json) return {};
  try {
    const parsed: unknown = JSON.parse(json);
    if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return {};
    const out: PlacementOverrides = {};
    for (const [k, v] of Object.entries(parsed)) if (v === "toolbar" || v === "menu") out[k] = v;
    return out;
  } catch {
    return {};
  }
}

/** Effective placement per item: explicit pins first, then defaults, capped at six on the toolbar. */
export function resolvePlacements(items: readonly PlaceableItem[], overrides: PlacementOverrides, max = MAX_TOOLBAR_PINS): Record<string, Placement> {
  const out: Record<string, Placement> = {};
  let used = 0;
  const wants = (i: PlaceableItem): Placement => overrides[i.id] ?? i.defaultPlacement;
  for (const i of items) {
    if (overrides[i.id] === "toolbar" && used < max) {
      out[i.id] = "toolbar";
      used++;
    }
  }
  for (const i of items) {
    if (out[i.id]) continue;
    if (wants(i) === "toolbar" && overrides[i.id] === undefined && used < max) {
      out[i.id] = "toolbar";
      used++;
    } else {
      out[i.id] = "menu";
    }
  }
  return out;
}

export type PlacementResult = { ok: true; overrides: PlacementOverrides } | { ok: false; reason: "limit" };

/**
 * Moves one item. Only differences from the item's default are stored; stale
 * pins of other models are trimmed first when the stored total would exceed
 * the cap, and pinning is refused when the toolbar of this model is full.
 */
export function setPlacement(items: readonly PlaceableItem[], overrides: PlacementOverrides, id: string, target: Placement, max = MAX_TOOLBAR_PINS): PlacementResult {
  const item = items.find((i) => i.id === id);
  if (!item) return { ok: false, reason: "limit" };
  const current = resolvePlacements(items, overrides, max);
  if (target === "toolbar") {
    const onToolbar = items.filter((i) => current[i.id] === "toolbar").length;
    if (current[id] !== "toolbar" && onToolbar >= max) return { ok: false, reason: "limit" };
  }
  const next: PlacementOverrides = { ...overrides };
  if (target === item.defaultPlacement) delete next[id];
  else next[id] = target;

  const pins = () => Object.entries(next).filter(([, v]) => v === "toolbar");
  if (pins().length > max) {
    const known = new Set(items.map((i) => i.id));
    for (const [k] of pins()) {
      if (pins().length <= max) break;
      if (!known.has(k)) delete next[k];
    }
    if (pins().length > max) return { ok: false, reason: "limit" };
  }
  return { ok: true, overrides: next };
}
