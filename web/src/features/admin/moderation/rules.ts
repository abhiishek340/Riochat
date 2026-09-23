import { problem, type Problem } from "../lib/problem";

export interface Policy {
  version: number;
  inputTextCategories: string[];
  inputImageCategories: string[];
  outputTextCategories: string[];
  outputImageCategories: string[];
}

export interface ModerationConfig {
  enabled: boolean;
  baseUrl: string;
  model: string;
  hasAPIKey: boolean;
  apiKeyMasked?: string;
  timeoutSeconds: number;
  maxConcurrency: number;
  queueCapacity: number;
  policy: Policy;
}

export interface CategoryCatalog {
  text: string[];
  image: string[];
}

export const POLICY_LISTS = ["inputTextCategories", "inputImageCategories", "outputTextCategories", "outputImageCategories"] as const;
export type PolicyList = (typeof POLICY_LISTS)[number];

export interface ServiceDraft {
  enabled: boolean;
  baseUrl: string;
  model: string;
  apiKey: string;
  timeoutSeconds: string;
  maxConcurrency: string;
  queueCapacity: string;
}

export const LIMITS = {
  timeoutSeconds: { min: 1, max: 60 },
  maxConcurrency: { min: 1, max: 64 },
  queueCapacity: { min: 1, max: 4096 },
} as const;

export function serviceDraftOf(config: ModerationConfig): ServiceDraft {
  return {
    enabled: config.enabled,
    baseUrl: config.baseUrl,
    model: config.model,
    apiKey: "",
    timeoutSeconds: String(config.timeoutSeconds),
    maxConcurrency: String(config.maxConcurrency),
    queueCapacity: String(config.queueCapacity),
  };
}

export function selectedCount(policy: Pick<Policy, PolicyList>): number {
  return POLICY_LISTS.reduce((sum, list) => sum + policy[list].length, 0);
}

export function hasAnyCategory(policy: Pick<Policy, PolicyList>): boolean {
  return selectedCount(policy) > 0;
}

/** True when anything differs from the saved service settings, or a key was typed. */
export function serviceDirty(draft: ServiceDraft, saved: ModerationConfig): boolean {
  const base = serviceDraftOf(saved);
  return draft.apiKey.trim() !== "" || (Object.keys(base) as Array<keyof ServiceDraft>).some((key) => key !== "apiKey" && draft[key] !== base[key]);
}

function whole(text: string): number | null {
  return /^\d+$/.test(text.trim()) ? Number(text.trim()) : null;
}

export function validateRange(field: keyof typeof LIMITS, text: string): Problem | null {
  const n = whole(text);
  const { min, max } = LIMITS[field];
  if (n === null || n < min || n > max) return problem(`adminModeration.problems.range`, { field: `adminModeration.service.${field}`, min, max });
  return null;
}

/**
 * Checks a service save. Disabling needs nothing. Enabling needs a key (typed
 * or already stored), valid numbers and, when it switches on, a category.
 */
export function validateService(draft: ServiceDraft, saved: ModerationConfig, policy: Pick<Policy, PolicyList>): Problem | null {
  if (!draft.enabled) return null;
  if (!saved.enabled && !hasAnyCategory(policy)) return problem("adminModeration.problems.noCategory");
  if (!draft.apiKey.trim() && !saved.hasAPIKey) return problem("adminModeration.problems.keyRequired");
  for (const field of ["timeoutSeconds", "maxConcurrency", "queueCapacity"] as const) {
    const bad = validateRange(field, draft[field]);
    if (bad) return bad;
  }
  return null;
}

export interface UpdateBody {
  enabled?: boolean;
  baseUrl?: string;
  model?: string;
  apiKey?: string;
  timeoutSeconds?: number;
  maxConcurrency?: number;
  queueCapacity?: number;
  policy?: Omit<Policy, "version">;
}

export function policyBody(policy: Pick<Policy, PolicyList>): Omit<Policy, "version"> {
  return {
    inputTextCategories: [...policy.inputTextCategories],
    inputImageCategories: [...policy.inputImageCategories],
    outputTextCategories: [...policy.outputTextCategories],
    outputImageCategories: [...policy.outputImageCategories],
  };
}

/** Disabling sends only the flag; enabling sends every service field, the key only when typed, and the policy on the switch-on transition. */
export function serviceBody(draft: ServiceDraft, saved: ModerationConfig, policy: Pick<Policy, PolicyList>): UpdateBody {
  if (!draft.enabled) return { enabled: false };
  const body: UpdateBody = {
    enabled: true,
    baseUrl: draft.baseUrl.trim(),
    model: draft.model.trim(),
    timeoutSeconds: Number(draft.timeoutSeconds.trim()),
    maxConcurrency: Number(draft.maxConcurrency.trim()),
    queueCapacity: Number(draft.queueCapacity.trim()),
  };
  if (draft.apiKey.trim()) body.apiKey = draft.apiKey.trim();
  if (!saved.enabled) body.policy = policyBody(policy);
  return body;
}

export function validatePolicy(policy: Pick<Policy, PolicyList>): Problem | null {
  return hasAnyCategory(policy) ? null : problem("adminModeration.problems.noCategory");
}

export function togglePolicyCategory(policy: Policy, list: PolicyList, category: string, on: boolean): Policy {
  const current = policy[list];
  const next = on ? (current.includes(category) ? current : [...current, category]) : current.filter((c) => c !== category);
  return { ...policy, [list]: next };
}

export function policyDirty(draft: Policy, saved: Policy): boolean {
  return POLICY_LISTS.some((list) => {
    const a = [...draft[list]].sort();
    const b = [...saved[list]].sort();
    return a.length !== b.length || a.some((x, i) => x !== b[i]);
  });
}

export interface ProbeTarget {
  valid: boolean;
  model?: string;
  latencyMS: number;
  error?: string;
}

export interface ProbeResult {
  text: ProbeTarget;
  image: ProbeTarget;
}

export function probeHealthy(result: ProbeResult): boolean {
  return result.text.valid && result.image.valid;
}

export interface StatItem {
  statDate: string;
  direction: string;
  modality: string;
  result: string;
  category: string;
  checkCount: number;
  contentItems: number;
  hitCount: number;
  failureCount: number;
  latencySumMS: number;
  latencyCount: number;
}

export interface StatTotals {
  checks: number;
  hits: number;
  failures: number;
  hitRate: number;
  avgLatencyMs: number | null;
}

export interface DayStats extends StatTotals {
  date: string;
}

export function totalsOf(items: readonly StatItem[]): StatTotals {
  let checks = 0;
  let hits = 0;
  let failures = 0;
  let latencySum = 0;
  let latencyCount = 0;
  for (const item of items) {
    checks += item.checkCount;
    hits += item.hitCount;
    failures += item.failureCount;
    latencySum += item.latencySumMS;
    latencyCount += item.latencyCount;
  }
  return { checks, hits, failures, hitRate: checks > 0 ? hits / checks : 0, avgLatencyMs: latencyCount > 0 ? latencySum / latencyCount : null };
}

/** Daily rows, oldest first. */
export function dailyStats(items: readonly StatItem[]): DayStats[] {
  const byDay = new Map<string, StatItem[]>();
  for (const item of items) {
    const key = item.statDate.slice(0, 10);
    byDay.set(key, [...(byDay.get(key) ?? []), item]);
  }
  return [...byDay.entries()].sort(([a], [b]) => a.localeCompare(b)).map(([date, rows]) => ({ date, ...totalsOf(rows) }));
}
