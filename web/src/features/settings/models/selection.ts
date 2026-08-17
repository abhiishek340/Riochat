/** Pure rules for choosing which models (and protocols) a provider exposes. */
import type { AvailableModel, ModelInput, ProviderModel } from "./api";

/** Catalogs up to this size start fully selected; bigger ones start empty. */
export const PRESELECT_LIMIT = 12;

export interface SelectedModel {
  name: string;
  protocols: string[];
}

export function preselect(models: readonly AvailableModel[]): SelectedModel[] {
  if (models.length === 0 || models.length > PRESELECT_LIMIT) return [];
  return models.map((m) => ({ name: m.name, protocols: [...m.suggestedProtocols] }));
}

/** Request entries: bare names unless the server takes per-model protocols. */
export function toRequestModels(selected: readonly SelectedModel[], perModelProtocols: boolean): ModelInput[] {
  return selected.map((m) => (perModelProtocols ? { name: m.name, protocols: [...m.protocols] } : m.name));
}

/** True when the chosen set, or any protocol list, differs from what is saved (names and protocols compared). */
export function modelsChanged(saved: readonly ProviderModel[], chosen: readonly SelectedModel[]): boolean {
  if (saved.length !== chosen.length) return true;
  const byName = new Map(saved.map((m) => [m.name, m.protocols.join("+")]));
  for (const m of chosen) {
    const before = byName.get(m.name);
    if (before === undefined || before !== m.protocols.join("+")) return true;
  }
  return false;
}

/**
 * The list the edit dialog offers: the provider's live models plus any
 * enabled model the provider no longer lists, so it can still be removed.
 */
export function mergeAvailable(live: readonly AvailableModel[], enabled: readonly ProviderModel[]): AvailableModel[] {
  const names = new Set(live.map((m) => m.name));
  const out = [...live];
  for (const m of enabled) {
    if (!names.has(m.name)) out.push({ name: m.name, suggestedProtocols: [...m.protocols] });
  }
  return out;
}

export interface PickerRow {
  name: string;
  /** Protocol value shown in the row's select (joined with "+"). */
  protocol: string;
  selected: boolean;
}

/**
 * Rows in a stable order: models selected when the picker opened come first
 * (in their given order), then the rest as listed. Every row carries a
 * protocol: the chosen/saved one when there is one, else the suggestion.
 */
export function buildRows(available: readonly AvailableModel[], selected: readonly SelectedModel[], fallbackProtocol = ""): PickerRow[] {
  const chosen = new Map(selected.map((m) => [m.name, m.protocols.join("+")]));
  const known = new Set(available.map((m) => m.name));
  const head: PickerRow[] = [];
  for (const m of selected) {
    if (known.has(m.name)) head.push({ name: m.name, protocol: m.protocols.join("+"), selected: true });
  }
  const tail: PickerRow[] = [];
  for (const m of available) {
    if (chosen.has(m.name)) continue;
    tail.push({ name: m.name, protocol: m.suggestedProtocols.join("+") || fallbackProtocol, selected: false });
  }
  return [...head, ...tail];
}

export function filterRows(rows: readonly PickerRow[], query: string, onlySelected: boolean, selectedNames: ReadonlySet<string>): PickerRow[] {
  const q = query.trim().toLowerCase();
  return rows.filter((r) => (!onlySelected || selectedNames.has(r.name)) && (!q || r.name.toLowerCase().includes(q)));
}

export type CheckState = "all" | "none" | "some";

export function visibleCheckState(visible: readonly PickerRow[], selectedNames: ReadonlySet<string>): CheckState {
  if (visible.length === 0) return "none";
  const n = visible.filter((r) => selectedNames.has(r.name)).length;
  return n === 0 ? "none" : n === visible.length ? "all" : "some";
}

/** Select-all/none over the visible rows: any state other than "all" selects them all. */
export function toggleVisible(visible: readonly PickerRow[], selectedNames: ReadonlySet<string>): Set<string> {
  const next = new Set(selectedNames);
  const everything = visibleCheckState(visible, selectedNames) === "all";
  for (const r of visible) {
    if (everything) next.delete(r.name);
    else next.add(r.name);
  }
  return next;
}
