/**
 * Turns the pricing snapshot stored with a usage entry into the lines of a
 * cost explanation. Amounts are nano-USD (1 USD = 1e9); token rates are per
 * million tokens. A damaged snapshot degrades to an empty one.
 */
import { isRecord } from "@/lib/utils/guards";

export const NANO = 1_000_000_000;

export type PricingMode = "token" | "call" | "duration" | "tiered";
export type Snapshot = Record<string, unknown>;

export function parseSnapshot(raw: string): Snapshot {
  if (!raw.trim()) return {};
  try {
    const data: unknown = JSON.parse(raw);
    return isRecord(data) ? data : {};
  } catch {
    return {};
  }
}

function n(snap: Snapshot, key: string): number {
  const v = snap[key];
  return typeof v === "number" && Number.isFinite(v) ? v : 0;
}
function has(snap: Snapshot, key: string): boolean {
  return typeof snap[key] === "number";
}

export type LineKind = "input" | "output" | "cacheRead" | "cacheWrite" | "call" | "duration";

export interface BreakdownLine {
  kind: LineKind;
  /** Tokens, calls or seconds. */
  quantity: number;
  /** Nano-USD per million tokens, per call or per second. */
  rateNano: number;
  billedNano: number;
}

export interface ServiceLine {
  name: string;
  calls: number;
  unitNano: number;
  billedNano: number;
}

export interface Breakdown {
  mode: PricingMode;
  lines: BreakdownLine[];
  services: ServiceLine[];
  /** Sum of the model lines. */
  baseNano: number;
  serviceNano: number;
  totalNano: number;
  free: boolean;
  multiplier: number | null;
  schedule: string;
  cacheWriteVariant: "5m" | "1h" | "mixed" | "";
  tier: { from: number; upTo: number | null } | null;
}

export function serviceItemsOf(snap: Snapshot): Snapshot[] {
  const items = snap.service_items;
  return Array.isArray(items) ? items.filter(isRecord) : [];
}

/** Amount of a token line: the stored billed value if positive, else tokens * rate / 1M. */
export function tokenAmount(tokens: number, ratePerMillion: number, billed: number): number {
  if (billed > 0) return billed;
  return Math.round((tokens * ratePerMillion) / 1_000_000);
}

export function serviceLines(snap: Snapshot): ServiceLine[] {
  return serviceItemsOf(snap).map((item) => {
    const calls = Math.max(1, n(item, "call_count"));
    const billed = n(item, "billed_nanousd");
    const given = n(item, "call_nanousd_per_call");
    const name = typeof item.service_name === "string" && item.service_name.trim() ? item.service_name.trim() : typeof item.service_code === "string" && item.service_code.trim() ? item.service_code.trim() : "";
    return { name, calls, unitNano: given > 0 ? given : Math.round(billed / calls), billedNano: billed };
  });
}

export interface LedgerFacts {
  isFreeModel: boolean;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  callCount: number;
  durationSeconds: number;
  billedNano: number;
}

export function buildBreakdown(snapshotJSON: string, facts: LedgerFacts, extraServices: ServiceLine[] = []): Breakdown {
  const snap = parseSnapshot(snapshotJSON);
  const rawMode = typeof snap.pricing_mode === "string" ? snap.pricing_mode : "token";
  const mode: PricingMode = rawMode === "call" || rawMode === "duration" || rawMode === "tiered" ? rawMode : "token";
  const lines: BreakdownLine[] = [];

  if (mode === "call") {
    const rate = n(snap, "call_nanousd_per_call");
    lines.push({ kind: "call", quantity: facts.callCount, rateNano: rate, billedNano: has(snap, "call_billed_nanousd") ? n(snap, "call_billed_nanousd") : Math.round(facts.callCount * rate) });
  } else if (mode === "duration") {
    const rate = n(snap, "duration_nanousd_per_second");
    lines.push({
      kind: "duration",
      quantity: facts.durationSeconds,
      rateNano: rate,
      billedNano: has(snap, "duration_billed_nanousd") ? n(snap, "duration_billed_nanousd") : Math.round(facts.durationSeconds * rate),
    });
  } else {
    const output = facts.outputTokens + facts.reasoningTokens;
    const spec: [LineKind, number, string, string][] = [
      ["input", facts.inputTokens, "input_nanousd_per_m_tokens", "input_billed_nanousd"],
      ["output", output, "output_nanousd_per_m_tokens", "output_billed_nanousd"],
      ["cacheRead", facts.cacheReadTokens, "cache_read_nanousd_per_m_tokens", "cache_read_billed_nanousd"],
      ["cacheWrite", facts.cacheWriteTokens, "cache_write_nanousd_per_m_tokens", "cache_write_billed_nanousd"],
    ];
    for (const [kind, tokens, rateKey, billedKey] of spec) {
      const rate = n(snap, rateKey);
      const billed = tokenAmount(tokens, rate, n(snap, billedKey));
      if (tokens > 0 || billed > 0) lines.push({ kind, quantity: tokens, rateNano: rate, billedNano: billed });
    }
  }

  const services = [...serviceLines(snap), ...extraServices];
  const baseNano = lines.reduce((s, l) => s + l.billedNano, 0);
  const serviceNano = services.reduce((s, l) => s + l.billedNano, 0);
  let totalNano = baseNano + serviceNano;
  const free = facts.isFreeModel || snap.is_free_model === true;
  // A free model with nothing billed reads zero; one that carries service charges is billed normally.
  if (free && serviceNano === 0) totalNano = 0;

  const multiplier = has(snap, "rate_multiplier") && n(snap, "rate_multiplier") !== 1 ? n(snap, "rate_multiplier") : null;
  const schedule = typeof snap.schedule_period_name === "string" ? snap.schedule_period_name : "";
  const tier = mode === "tiered" ? { from: n(snap, "tiered_from_tokens"), upTo: typeof snap.tiered_up_to_tokens === "number" ? (snap.tiered_up_to_tokens as number) : null } : null;
  const variant: Breakdown["cacheWriteVariant"] = n(snap, "cache_write_1h_tokens") > 0 && n(snap, "cache_write_5m_tokens") > 0 ? "mixed" : n(snap, "cache_write_1h_tokens") > 0 ? "1h" : n(snap, "cache_write_5m_tokens") > 0 ? "5m" : "";

  return { mode, lines, services, baseNano, serviceNano, totalNano, free, multiplier, schedule, cacheWriteVariant: variant, tier };
}

/** USD value of nano-USD. */
export function nanoToUsd(nano: number): number {
  return nano / NANO;
}
