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
