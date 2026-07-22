/** Remote model sync: merging, default choices and the two-step apply. */
import { kindsForProtocols, kindsJson, parseKinds, sortProtocols } from "../lib/protocols";
import type { ImportItem, RemoteItem, SyncPlan, SyncResult } from "../llm-api";

export interface SyncRow {
  upstreamModelName: string;
  suggestedPlatformModelName: string;
  protocols: string[];
  kindsJSON: string;
  alreadyBound: boolean;
  alreadySynced: boolean;
  boundPlatformModels: string[];
  status: string;
  /** Present only in the plan, not in the remote list (it will be inactivated). */
  vanished: boolean;
}

/**
 * One row per upstream model name: suggestions merge and protocols union.
 * Unbound models come first, bound ones after, and models the plan will
 * inactivate follow as extra rows.
 */
export function buildRows(items: readonly RemoteItem[], plan: Pick<SyncPlan, "inactivatedModels">): SyncRow[] {
  const map = new Map<string, SyncRow>();
  for (const item of items) {
    const existing = map.get(item.upstreamModelName);
    const protocols = item.suggestedProtocols?.length ? item.suggestedProtocols : item.suggestedProtocol ? [item.suggestedProtocol] : [];
    if (!existing) {
      map.set(item.upstreamModelName, {
        upstreamModelName: item.upstreamModelName,
        suggestedPlatformModelName: item.suggestedPlatformModelName,
        protocols: sortProtocols(protocols),
        kindsJSON: item.suggestedKindsJSON,
        alreadyBound: item.alreadyBound,
        alreadySynced: item.alreadySynced,
        boundPlatformModels: [...(item.boundPlatformModels ?? [])],
        status: item.upstreamModelStatus,
        vanished: false,
      });
      continue;
    }
    existing.protocols = sortProtocols([...existing.protocols, ...protocols]);
    existing.suggestedPlatformModelName ||= item.suggestedPlatformModelName;
    existing.kindsJSON ||= item.suggestedKindsJSON;
    existing.alreadyBound ||= item.alreadyBound;
    existing.alreadySynced ||= item.alreadySynced;
    existing.boundPlatformModels = [...new Set([...existing.boundPlatformModels, ...(item.boundPlatformModels ?? [])])];
  }
  const rows = [...map.values()].sort((a, b) => Number(a.alreadyBound) - Number(b.alreadyBound));
  for (const name of plan.inactivatedModels ?? []) {
    if (!map.has(name)) rows.push({ upstreamModelName: name, suggestedPlatformModelName: "", protocols: [], kindsJSON: "", alreadyBound: false, alreadySynced: true, boundPlatformModels: [], status: "inactive", vanished: true });
  }
  return rows;
}

/** Unbound models with at least one suggested protocol: binding needs a protocol. */
export function defaultSelection(rows: readonly SyncRow[]): Set<string> {
  return new Set(rows.filter((r) => !r.alreadyBound && !r.vanished && r.protocols.length > 0).map((r) => r.upstreamModelName));
}

/** The search covers name, suggested platform name, protocols and status. */
export function searchRows(rows: readonly SyncRow[], query: string): SyncRow[] {
  const q = query.trim().toLowerCase();
  if (!q) return [...rows];
  return rows.filter((r) => [r.upstreamModelName, r.suggestedPlatformModelName, r.status, ...r.protocols].some((x) => x.toLowerCase().includes(q)));
}

export type Outcome = keyof SyncPlan;
export const OUTCOMES: readonly Outcome[] = ["addedModels", "updatedModels", "reactivatedModels", "inactivatedModels", "unchangedModels", "protectedModels", "unresolvedProtocolModels"];

export function outcomeCounts(plan: SyncPlan): Record<Outcome, number> {
  return Object.fromEntries(OUTCOMES.map((k) => [k, plan[k]?.length ?? 0])) as Record<Outcome, number>;
}

/** Rows belonging to the chosen outcome chips; no chip means every row. */
export function rowsForOutcomes(rows: readonly SyncRow[], plan: SyncPlan, chosen: ReadonlySet<Outcome>): SyncRow[] {
  if (chosen.size === 0) return [...rows];
  const names = new Set<string>();
  for (const o of chosen) for (const n of plan[o] ?? []) names.add(n);
  return rows.filter((r) => names.has(r.upstreamModelName));
}

export function hasCatalogChanges(plan: SyncPlan): boolean {
  return plan.addedModels.length + plan.updatedModels.length + plan.reactivatedModels.length + plan.inactivatedModels.length > 0;
}

/** The remote list being empty needs an explicit allowance on sync. */
export function allowEmpty(items: readonly unknown[]): boolean {
  return items.length === 0;
}

export function needsConfirmation(plan: SyncPlan): boolean {
  return (plan.inactivatedModels?.length ?? 0) > 0;
}

/** The apply button is useless without a snapshot and a plan, or when nothing would happen. */
export function canApply(args: { loading: boolean; importing: boolean; snapshotId: string; plan: SyncPlan | null; selected: number }): boolean {
  if (args.loading || args.importing || !args.snapshotId || !args.plan) return false;
  return hasCatalogChanges(args.plan) || args.selected > 0;
}

/**
 * Step two imports the chosen items that step one confirmed. Each carries its
 * edited platform model name (the suggestion, else the upstream name) and
 * protocols, with kinds derived from them.
 */
export function importItems(rows: readonly SyncRow[], selected: ReadonlySet<string>, names: Readonly<Record<string, string>>, synced: SyncResult["syncedModels"]): ImportItem[] {
  const present = new Set(synced.map((m) => m.upstreamModelName));
  const out: ImportItem[] = [];
  for (const row of rows) {
    if (!selected.has(row.upstreamModelName) || !present.has(row.upstreamModelName) || row.vanished) continue;
    const platform = (names[row.upstreamModelName] ?? row.suggestedPlatformModelName ?? "").trim() || row.upstreamModelName;
    const item: ImportItem = { upstreamModelName: row.upstreamModelName, platformModelName: platform };
    if (row.protocols.length > 0) {
      item.protocols = row.protocols.slice(0, 2);
      item.kindsJSON = kindsJson(kindsForProtocols(item.protocols));
    } else if (row.kindsJSON && parseKinds(row.kindsJSON).length > 0) {
      item.kindsJSON = row.kindsJSON;
    }
    out.push(item);
  }
  return out;
}

export function syncSummary(r: Pick<SyncResult, "createdUpstreamModels" | "updatedUpstreamModels" | "reactivatedModels" | "inactivatedModels" | "totalUpstream">): { created: number; updated: number; reactivated: number; inactivated: number; total: number } {
  return { created: r.createdUpstreamModels, updated: r.updatedUpstreamModels, reactivated: r.reactivatedModels, inactivated: Number(r.inactivatedModels), total: r.totalUpstream };
}
