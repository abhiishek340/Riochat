/** Suggestions from the public OpenRouter pricing catalog. */
import type { TierForm } from "./pricing";

export interface OfficialPrices {
  prompt: string;
  completion: string;
  inputCacheRead?: string;
  inputCacheWrite?: string;
  cacheWritePriceBasis?: string;
  overrides?: Array<{ minPromptTokens: number; prompt?: string; completion?: string; inputCacheRead?: string; inputCacheWrite?: string }>;
  unsupportedFields?: string[];
}

export interface OfficialItem {
  id: string;
  canonicalSlug: string;
  name: string;
  contextLength?: number;
  maxCompletionTokens?: number;
  pricing: OfficialPrices;
}

export interface OfficialCatalog {
  items: OfficialItem[];
  fetchedAt: string;
  origin: "remote" | "builtin" | (string & {});
  cached: boolean;
  stale: boolean;
}

const round6 = (n: number) => Math.round(n * 1e6) / 1e6;

/** A per-token decimal string as dollars per million tokens, scaled and rounded to six decimals. */
export function perMillion(perToken: string | undefined, multiplier: number): number | null {
  if (perToken === undefined || perToken.trim() === "") return null;
  const n = Number(perToken);
  if (!Number.isFinite(n) || n < 0) return null;
  return round6(n * 1e6 * multiplier);
}

export interface Converted {
  isFree: boolean;
  mode: "token" | "tiered";
  input: number;
  cacheRead: number;
  cacheWrite: number;
  output: number;
  cacheWriteBasis: string;
  tiers: TierForm[];
  ignored: string[];
}

export type Conversion = { ok: true; value: Converted } | { ok: false; missing: Array<"prompt" | "completion"> };

/** Prompt and completion prices are required; overrides with rising thresholds become tiers. */
export function convertOfficial(item: OfficialItem, multiplier: number): Conversion {
  const m = Number.isFinite(multiplier) && multiplier > 0 ? multiplier : 1;
  const p = item.pricing;
  const input = perMillion(p.prompt, m);
  const output = perMillion(p.completion, m);
  const missing: Array<"prompt" | "completion"> = [];
  if (input === null) missing.push("prompt");
  if (output === null) missing.push("completion");
  if (input === null || output === null) return { ok: false, missing };
  const cacheRead = perMillion(p.inputCacheRead, m) ?? 0;
  const cacheWrite = perMillion(p.inputCacheWrite, m) ?? 0;
  const overrides = [...(p.overrides ?? [])].filter((o) => Number.isFinite(o.minPromptTokens) && o.minPromptTokens > 0).sort((a, b) => a.minPromptTokens - b.minPromptTokens);
  let tiers: TierForm[] = [];
  if (overrides.length > 0) {
    let current = { input, cacheRead, cacheWrite, output };
    tiers = overrides.map((o) => {
      const tier: TierForm = { upToTokens: String(o.minPromptTokens), input: String(current.input), cacheRead: String(current.cacheRead), cacheWrite: String(current.cacheWrite), output: String(current.output) };
      current = {
        input: perMillion(o.prompt, m) ?? current.input,
        cacheRead: perMillion(o.inputCacheRead, m) ?? current.cacheRead,
        cacheWrite: perMillion(o.inputCacheWrite, m) ?? current.cacheWrite,
        output: perMillion(o.completion, m) ?? current.output,
      };
      return tier;
    });
    tiers.push({ upToTokens: "0", input: String(current.input), cacheRead: String(current.cacheRead), cacheWrite: String(current.cacheWrite), output: String(current.output) });
  }
  const prices = tiers.length > 0 ? tiers.flatMap((t) => [Number(t.input), Number(t.cacheRead), Number(t.cacheWrite), Number(t.output)]) : [input, cacheRead, cacheWrite, output];
  return {
    ok: true,
    value: {
      isFree: prices.every((n) => n === 0),
      mode: tiers.length > 0 ? "tiered" : "token",
      input,
      cacheRead,
      cacheWrite,
      output,
      cacheWriteBasis: p.cacheWritePriceBasis === "anthropic_5m" ? "anthropic_5m" : "direct",
      tiers,
      ignored: p.unsupportedFields ?? [],
    },
  };
}

// --- matching ------------------------------------------------------------------------

function squash(text: string): string {
  return text.toLowerCase().replace(/[^a-z0-9]+/g, "");
}

function words(text: string): string[] {
  return text
    .toLowerCase()
    .split(/[^a-z0-9]+/)
    .filter(Boolean);
}

function similarity(a: string[], b: string[]): number {
  if (a.length === 0 || b.length === 0) return 0;
  const left = new Set(a);
  const right = new Set(b);
  let both = 0;
  for (const w of left) if (right.has(w)) both++;
  return both / (left.size + right.size - both);
}

const EXACT = 100;
const NORMALIZED = 90;
const SUFFIX = 80;
const FUZZY_BASE = 30;
const FUZZY_SPAN = 45;
const SUGGEST_CUTOFF = 0.5;
const SEARCH_CUTOFF = 0.34;

export interface Ranked {
  item: OfficialItem;
  score: number;
}

function vendorPrefix(item: OfficialItem): string {
  const slash = item.id.indexOf("/");
  return slash > 0 ? item.id.slice(0, slash).toLowerCase() : "";
}

function scoreOne(name: string, vendor: string, item: OfficialItem, cutoff: number): number {
  const n = name.trim().toLowerCase();
  if (!n) return 0;
  const ids = [item.id, item.canonicalSlug].filter(Boolean).map((s) => s.toLowerCase());
  const bonus = vendor && vendorPrefix(item) === vendor.trim().toLowerCase() ? 5 : 0;
  if (ids.includes(n)) return EXACT;
  const flat = squash(n);
  if ([...ids, item.name.toLowerCase()].some((s) => squash(s) === flat)) return NORMALIZED;
  const tail = (s: string) => squash(s.slice(s.lastIndexOf("/") + 1));
  if (ids.some((s) => tail(s) === flat) || tail(item.name) === flat) return SUFFIX + bonus;
  const sim = Math.max(similarity(words(n), words(item.id.slice(item.id.indexOf("/") + 1))), similarity(words(n), words(item.name)));
  if (sim < cutoff) return 0;
  // Fuzzy matches always rank below exact ones.
  return Math.min(FUZZY_BASE + FUZZY_SPAN * sim + bonus, SUFFIX - 1);
}

/** Catalog entries that plausibly describe a platform model, best first. */
export function suggest(modelName: string, vendor: string, items: readonly OfficialItem[], limit = 8): Ranked[] {
  return items
    .map((item) => ({ item, score: scoreOne(modelName, vendor, item, SUGGEST_CUTOFF) }))
    .filter((r) => r.score > 0)
    .sort((a, b) => b.score - a.score || a.item.id.localeCompare(b.item.id))
    .slice(0, limit);
}

/** Free-text catalog search: substrings rank high, fuzzy matches follow with a gentler cut-off. */
export function searchCatalog(query: string, items: readonly OfficialItem[], limit = 20): OfficialItem[] {
  const q = query.trim().toLowerCase();
  if (!q) return items.slice(0, limit);
  const flat = squash(q);
  const scored = items.map((item) => {
    const hay = `${item.id} ${item.name}`.toLowerCase();
    if (hay.includes(q) || squash(hay).includes(flat)) return { item, score: 200 };
    return { item, score: scoreOne(q, "", item, SEARCH_CUTOFF) };
  });
  return scored
    .filter((r) => r.score > 0)
    .sort((a, b) => b.score - a.score || a.item.id.localeCompare(b.item.id))
    .slice(0, limit)
    .map((r) => r.item);
}
