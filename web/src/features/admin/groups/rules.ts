/** Pure rules of the permission groups screen. */
import type { GroupRule } from "../llm-api";

/** A decimal factor to whole percent; unparseable or non-positive input means 1.0x. */
export function percentFromFactor(text: string): number {
  const n = Number(text.trim());
  if (!Number.isFinite(n) || n <= 0) return 100;
  const percent = Math.round(n * 100);
  return percent > 0 ? percent : 100;
}

/** Stored percent shown as a factor; a stored 0 shows as 1. */
export function factorFromPercent(percent: number): string {
  const p = percent > 0 ? percent : 100;
  return String(p / 100);
}

export const RULE_TYPES = ["all", "vendor", "protocol", "upstream"] as const;

export function sameRule(a: GroupRule, b: GroupRule): boolean {
  return a.type === b.type && a.value === b.value;
}

export function ruleKey(rule: GroupRule): string {
  return `${rule.type}:${rule.value}`;
}

/**
 * A draft rule can be added when it needs a value and has one, does not repeat
 * an existing rule, and no `all` rule exists yet (after that, only `all`
 * stays, so nothing else may be added).
 */
export function canAddRule(rules: readonly GroupRule[], draft: GroupRule): boolean {
  const normalized: GroupRule = draft.type === "all" ? { type: "all", value: "" } : { type: draft.type, value: draft.value.trim() };
  if (normalized.type !== "all" && !normalized.value) return false;
  if (rules.some((r) => r.type === "all")) return false;
  return !rules.some((r) => sameRule(r, normalized));
}

export function addRule(rules: readonly GroupRule[], draft: GroupRule): GroupRule[] {
  if (!canAddRule(rules, draft)) return [...rules];
  return [...rules, draft.type === "all" ? { type: "all", value: "" } : { type: draft.type, value: draft.value.trim() }];
}

export function removeRule(rules: readonly GroupRule[], rule: GroupRule): GroupRule[] {
  return rules.filter((r) => !sameRule(r, rule));
}

export interface SaveStages {
  group: boolean;
  models: boolean;
  users: boolean;
}

/** What to refresh after a save that stopped at some stage. */
export function refreshAfter(done: SaveStages): { reloadList: boolean; invalidateCache: boolean } {
  return { reloadList: done.group, invalidateCache: done.models };
}

export function deletionSummary(s: { manualModelCount: number; ruleCount: number; manualUserCount: number }): { models: number; rules: number; users: number } {
  return { models: s.manualModelCount, rules: s.ruleCount, users: s.manualUserCount };
}
