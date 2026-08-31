/** Model pricing: the form, what is stored, and the JSON import/export format. */
import { isPlainObject } from "../lib/json";
import { canonicalizeTime, parseSchedule, scheduleJson, validateSchedule, type SchedulePeriod } from "./schedule";
import type { ModelPrice } from "../lib/wire";

export const PRICING_MODES = ["token", "call", "duration", "tiered"] as const;
export type PricingMode = (typeof PRICING_MODES)[number];
export const CACHE_BASES = ["direct", "anthropic_5m"] as const;

export interface TierForm {
  /** Prompt-token ceiling of the tier; 0 or blank means no upper limit (the last tier). */
  upToTokens: string;
  input: string;
  cacheRead: string;
  cacheWrite: string;
  output: string;
}

export interface PricingForm {
  isFree: boolean;
  mode: PricingMode;
  input: string;
  cacheRead: string;
  cacheWrite: string;
  cacheWriteBasis: string;
  output: string;
  call: string;
  duration: string;
  tiers: TierForm[];
  schedule: SchedulePeriod[];
}

/** Negative or non-numeric prices count as zero. */
export function parsePrice(text: string | number | null | undefined): number {
  const n = typeof text === "number" ? text : Number((text ?? "").toString().trim());
  return Number.isFinite(n) && n > 0 ? n : 0;
}

export function defaultTiers(): TierForm[] {
  return [
    { upToTokens: "200000", input: "0", cacheRead: "0", cacheWrite: "0", output: "0" },
    { upToTokens: "0", input: "0", cacheRead: "0", cacheWrite: "0", output: "0" },
  ];
}

export function isPricingMode(value: string): value is PricingMode {
  return (PRICING_MODES as readonly string[]).includes(value);
}

export function parseTiers(text: string | null | undefined): TierForm[] {
  if (!text) return [];
  try {
    const root: unknown = JSON.parse(text);
    const tiers = isPlainObject(root) ? root.tiers : undefined;
    if (!Array.isArray(tiers)) return [];
    return tiers.flatMap((raw): TierForm[] => {
      if (!isPlainObject(raw)) return [];
      const s = (v: unknown) => (typeof v === "number" && Number.isFinite(v) ? String(v) : "0");
      return [{ upToTokens: s(raw.upToTokens), input: s(raw.inputUSDPerMTokens), cacheRead: s(raw.cacheReadUSDPerMTokens), cacheWrite: s(raw.cacheWriteUSDPerMTokens), output: s(raw.outputUSDPerMTokens) }];
    });
  } catch {
    return [];
  }
}

export function tiersJson(tiers: readonly TierForm[]): string {
  return JSON.stringify({
    tiers: tiers.map((t) => ({
      upToTokens: Math.floor(parsePrice(t.upToTokens)),
      inputUSDPerMTokens: parsePrice(t.input),
      cacheReadUSDPerMTokens: parsePrice(t.cacheRead),
      cacheWriteUSDPerMTokens: parsePrice(t.cacheWrite),
      outputUSDPerMTokens: parsePrice(t.output),
    })),
  });
}

const show = (n: number | undefined) => String(n ?? 0);

export function formFromPrice(price: ModelPrice | undefined): PricingForm {
  if (!price) {
    return { isFree: false, mode: "token", input: "0", cacheRead: "0", cacheWrite: "0", cacheWriteBasis: "direct", output: "0", call: "0", duration: "0", tiers: defaultTiers(), schedule: [] };
  }
  const tiers = parseTiers(price.tieredPricingJSON);
  return {
    isFree: price.isFree,
    mode: isPricingMode(price.pricingMode) ? price.pricingMode : "token",
    input: show(price.inputUSDPerMTokens),
    cacheRead: show(price.cacheReadUSDPerMTokens),
    cacheWrite: show(price.cacheWriteUSDPerMTokens),
    cacheWriteBasis: price.cacheWritePriceBasis === "anthropic_5m" ? "anthropic_5m" : "direct",
    output: show(price.outputUSDPerMTokens),
    call: show(price.callUSDPerCall),
    duration: show(price.durationUSDPerSecond),
    tiers: tiers.length > 0 ? tiers : defaultTiers(),
    schedule: parseSchedule(price.schedulePricingJSON),
  };
}

export interface PriceBody {
  platformModelName: string;
  currency: string;
  isFree: boolean;
  pricingMode: PricingMode;
  inputUSDPerMTokens: number;
  cacheReadUSDPerMTokens: number;
  cacheWriteUSDPerMTokens: number;
  cacheWritePriceBasis: string;
  outputUSDPerMTokens: number;
  callUSDPerCall: number;
  durationUSDPerSecond: number;
  tieredPricingJSON: string;
  schedulePricingJSON: string;
}

/** Prices that do not belong to the chosen mode are stored as zero. */
export function bodyFromForm(name: string, form: PricingForm): PriceBody {
  const token = form.mode === "token";
  return {
    platformModelName: name,
    currency: "USD",
    isFree: form.isFree,
    pricingMode: form.mode,
    inputUSDPerMTokens: token ? parsePrice(form.input) : 0,
    cacheReadUSDPerMTokens: token ? parsePrice(form.cacheRead) : 0,
    cacheWriteUSDPerMTokens: token ? parsePrice(form.cacheWrite) : 0,
    cacheWritePriceBasis: form.cacheWriteBasis === "anthropic_5m" ? "anthropic_5m" : "direct",
    outputUSDPerMTokens: token ? parsePrice(form.output) : 0,
    callUSDPerCall: form.mode === "call" ? parsePrice(form.call) : 0,
    durationUSDPerSecond: form.mode === "duration" ? parsePrice(form.duration) : 0,
    tieredPricingJSON: form.mode === "tiered" ? tiersJson(form.tiers) : "",
    schedulePricingJSON: scheduleJson(form.schedule),
  };
}

/** The existing record re-sent with only the free flag changed (or defaults for a model with no record). */
export function freeToggleBody(name: string, price: ModelPrice | undefined, isFree: boolean): PriceBody {
  return bodyFromForm(name, { ...formFromPrice(price), isFree });
}

export function isVideoModel(kindsJson: string | null | undefined): boolean {
  try {
    const kinds: unknown = JSON.parse(kindsJson || "[]");
    return Array.isArray(kinds) && (kinds.includes("video_gen") || kinds.includes("video_extension"));
  } catch {
    return false;
  }
}

// --- rows ----------------------------------------------------------------------------

export interface PricedModel {
  name: string;
  vendor: string;
  icon: string;
  kindsJSON: string;
  sortOrder: number;
}

/** Models without a pricing record float above the rest; otherwise the admin order holds. */
export function orderRows<T extends { name: string; sortOrder: number }>(rows: readonly T[], priced: ReadonlySet<string>): T[] {
  return [...rows].sort((a, b) => Number(priced.has(a.name)) - Number(priced.has(b.name)) || a.sortOrder - b.sortOrder);
}

export interface PricingFilters {
  q: string;
  vendor: string;
  mode: string;
  free: "" | "free" | "paid";
  configured: "" | "configured" | "unconfigured";
}

export function filterRows<T extends { name: string; vendor: string }>(rows: readonly T[], prices: ReadonlyMap<string, ModelPrice>, f: PricingFilters): T[] {
  const needle = f.q.trim().toLowerCase();
  return rows.filter((row) => {
    const price = prices.get(row.name);
    if (needle && !row.name.toLowerCase().includes(needle)) return false;
    if (f.vendor && row.vendor !== f.vendor) return false;
    if (f.mode && price?.pricingMode !== f.mode) return false;
    if (f.free === "free" && !price?.isFree) return false;
    if (f.free === "paid" && (!price || price.isFree)) return false;
    if (f.configured === "configured" && !price) return false;
    if (f.configured === "unconfigured" && price) return false;
    return true;
  });
}

// --- export / import ------------------------------------------------------------------------

export interface ExportEntry {
  currency: string;
  isFree: boolean;
  pricingMode: PricingMode;
  inputUSDPerMTokens: number;
  cacheReadUSDPerMTokens: number;
  cacheWriteUSDPerMTokens: number;
  cacheWritePriceBasis?: string;
  outputUSDPerMTokens: number;
  callUSDPerCall: number;
  durationUSDPerSecond: number;
  tieredPricing?: unknown;
  schedulePricing?: unknown;
}

/** An object keyed by model name (sorted); prices outside the entry's mode are zero. */
export function exportPricing(prices: readonly ModelPrice[]): Record<string, ExportEntry> {
  const out: Record<string, ExportEntry> = {};
  for (const price of [...prices].sort((a, b) => a.platformModelName.localeCompare(b.platformModelName))) {
    const body = bodyFromForm(price.platformModelName, formFromPrice(price));
    const entry: ExportEntry = {
      currency: body.currency,
      isFree: body.isFree,
      pricingMode: body.pricingMode,
      inputUSDPerMTokens: body.inputUSDPerMTokens,
      cacheReadUSDPerMTokens: body.cacheReadUSDPerMTokens,
      cacheWriteUSDPerMTokens: body.cacheWriteUSDPerMTokens,
      outputUSDPerMTokens: body.outputUSDPerMTokens,
      callUSDPerCall: body.callUSDPerCall,
      durationUSDPerSecond: body.durationUSDPerSecond,
    };
    if (price.cacheWritePriceBasis) entry.cacheWritePriceBasis = price.cacheWritePriceBasis;
    if (body.pricingMode === "tiered" && body.tieredPricingJSON) entry.tieredPricing = JSON.parse(body.tieredPricingJSON);
    if (body.schedulePricingJSON) entry.schedulePricing = JSON.parse(body.schedulePricingJSON);
    out[price.platformModelName] = entry;
  }
  return out;
}

export type ImportErrorKind =
  | "syntax"
  | "notObject"
  | "emptyName"
  | "duplicate"
  | "entryNotObject"
  | "mode"
  | "basis"
  | "durationNotVideo"
  | "number"
  | "tiers"
  | "tierLimit"
  | "schedule"
  | "unknownModels"
  | "empty";

export interface ImportError {
  kind: ImportErrorKind;
  name?: string;
  field?: string;
  /** For unknown models: the first few names and how many more there are. */
  names?: string[];
  more?: number;
}

export interface ImportPlan {
  body: PriceBody;
}

export type ImportResult = { ok: true; entries: PriceBody[] } | { ok: false; errors: ImportError[] };

const NUMBER_FIELDS = ["inputUSDPerMTokens", "cacheReadUSDPerMTokens", "cacheWriteUSDPerMTokens", "outputUSDPerMTokens", "callUSDPerCall", "durationUSDPerSecond"] as const;

function numberField(entry: Record<string, unknown>, key: string): number | "bad" {
  const v = entry[key];
  if (v === undefined || v === null || v === "") return 0;
  if (typeof v !== "number" || !Number.isFinite(v) || v < 0) return "bad";
  return v;
}

/**
 * Checks an import file against the models that can be priced. Unknown names
 * abort the whole import (the error lists up to five and counts the rest).
 */
export function parseImport(text: string, known: ReadonlyMap<string, { kindsJSON: string }>): ImportResult {
  let root: unknown;
  try {
    root = JSON.parse(text);
  } catch {
    return { ok: false, errors: [{ kind: "syntax" }] };
  }
  if (!isPlainObject(root)) return { ok: false, errors: [{ kind: "notObject" }] };
  const errors: ImportError[] = [];
  const seen = new Set<string>();
  const unknown: string[] = [];
  const entries: PriceBody[] = [];
  for (const [rawName, raw] of Object.entries(root)) {
    const name = rawName.trim();
    if (!name) {
      errors.push({ kind: "emptyName" });
      continue;
    }
    if (seen.has(name)) {
      errors.push({ kind: "duplicate", name });
      continue;
    }
    seen.add(name);
    if (!isPlainObject(raw)) {
      errors.push({ kind: "entryNotObject", name });
      continue;
    }
    const model = known.get(name);
    if (!model) {
      unknown.push(name);
      continue;
    }
    const mode = raw.pricingMode;
    if (typeof mode !== "string" || !isPricingMode(mode)) {
      errors.push({ kind: "mode", name });
      continue;
    }
    const basis = raw.cacheWritePriceBasis;
    if (basis !== undefined && basis !== "direct" && basis !== "anthropic_5m") {
      errors.push({ kind: "basis", name });
      continue;
    }
    if (mode === "duration" && !isVideoModel(model.kindsJSON)) {
      errors.push({ kind: "durationNotVideo", name });
      continue;
    }
    const numbers: Record<string, number> = {};
    let badField: string | null = null;
    for (const key of NUMBER_FIELDS) {
      const value = numberField(raw, key);
      if (value === "bad") {
        badField = key;
        break;
      }
      numbers[key] = value;
    }
    if (badField) {
      errors.push({ kind: "number", name, field: badField });
      continue;
    }
    let tieredText = "";
    if (mode === "tiered") {
      const tiered = raw.tieredPricing ?? (typeof raw.tieredPricingJSON === "string" ? safeParse(raw.tieredPricingJSON) : undefined);
      const tiers = isPlainObject(tiered) ? tiered.tiers : undefined;
      if (!Array.isArray(tiers) || tiers.length === 0) {
        errors.push({ kind: "tiers", name });
        continue;
      }
      let bad = false;
      for (const tier of tiers) {
        const limit = isPlainObject(tier) ? tier.upToTokens : undefined;
        if (!isPlainObject(tier) || (limit !== undefined && (typeof limit !== "number" || !Number.isFinite(limit) || limit < 0))) bad = true;
      }
      if (bad) {
        errors.push({ kind: "tierLimit", name });
        continue;
      }
      tieredText = JSON.stringify({ tiers });
    }
    const scheduleSource = raw.schedulePricing ?? (typeof raw.schedulePricingJSON === "string" ? safeParse(raw.schedulePricingJSON) : undefined);
    let scheduleText = "";
    if (scheduleSource !== undefined && scheduleSource !== null && scheduleSource !== "") {
      const periods = parseSchedule(JSON.stringify(scheduleSource));
      if (periods.length === 0 || validateSchedule(periods).length > 0 || periods.some((p) => canonicalizeTime(p.start) === null)) {
        errors.push({ kind: "schedule", name });
        continue;
      }
      scheduleText = scheduleJson(periods);
    }
    entries.push({
      platformModelName: name,
      currency: typeof raw.currency === "string" && raw.currency.trim() ? raw.currency.trim() : "USD",
      isFree: raw.isFree === true,
      pricingMode: mode,
      inputUSDPerMTokens: numbers.inputUSDPerMTokens ?? 0,
      cacheReadUSDPerMTokens: numbers.cacheReadUSDPerMTokens ?? 0,
      cacheWriteUSDPerMTokens: numbers.cacheWriteUSDPerMTokens ?? 0,
      cacheWritePriceBasis: basis === "anthropic_5m" ? "anthropic_5m" : "direct",
      outputUSDPerMTokens: numbers.outputUSDPerMTokens ?? 0,
      callUSDPerCall: numbers.callUSDPerCall ?? 0,
      durationUSDPerSecond: numbers.durationUSDPerSecond ?? 0,
      tieredPricingJSON: tieredText,
      schedulePricingJSON: scheduleText,
    });
  }
  if (unknown.length > 0) return { ok: false, errors: [{ kind: "unknownModels", names: unknown.slice(0, 5), more: Math.max(0, unknown.length - 5) }] };
  if (errors.length > 0) return { ok: false, errors };
  if (entries.length === 0) return { ok: false, errors: [{ kind: "empty" }] };
  return { ok: true, entries };
}

function safeParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}
