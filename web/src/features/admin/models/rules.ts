/** Pure rules of the Models page: source availability, list effects of toggles, forms and request bodies. */
import { isPlainObject } from "../lib/json";
import { problem, type Problem } from "../lib/problem";
import { kindsJson, PROTOCOLS, PROTOCOLS_BY_KIND, sortProtocols, type Kind } from "../lib/protocols";
import type { PlatformModel } from "../lib/wire";

// --- availability ----------------------------------------------------------------------------

export interface SourceState {
  status: string;
  upstreamStatus: string;
  upstreamModelStatus: string;
  circuitOpen: boolean;
}

/**
 * A source serves traffic when the model, the source, its upstream and the
 * upstream model are active and its circuit is closed. With the global
 * circuit breaker off, circuit state is ignored.
 */
export function sourceAvailable(modelStatus: string, source: SourceState, breakerOn = true): boolean {
  return modelStatus === "active" && source.status === "active" && source.upstreamStatus === "active" && source.upstreamModelStatus === "active" && !(breakerOn && source.circuitOpen);
}

export function clampActive(model: Pick<PlatformModel, "sourceCount">, active: number): number {
  return Math.max(0, Math.min(model.sourceCount, active));
}

/** The model after one of its sources changed state; the active count moves by one and stays within 0..sourceCount. */
export function withSourceChange(model: PlatformModel, before: SourceState, after: SourceState, breakerOn = true): PlatformModel {
  const was = sourceAvailable(model.status, before, breakerOn);
  const now = sourceAvailable(model.status, after, breakerOn);
  if (was === now) return model;
  return { ...model, activeSourceCount: clampActive(model, model.activeSourceCount + (now ? 1 : -1)) };
}

/** The source row removed: the count falls with it. */
export function withSourceRemoved(model: PlatformModel, source: SourceState, breakerOn = true): PlatformModel {
  const available = sourceAvailable(model.status, source, breakerOn);
  const sourceCount = Math.max(0, model.sourceCount - 1);
  const activeSourceCount = Math.max(0, Math.min(sourceCount, model.activeSourceCount - (available ? 1 : 0)));
  return { ...model, sourceCount, activeSourceCount };
}

export type SourceBadge = "none" | "notEnabled" | "ok";

export function sourceBadge(model: Pick<PlatformModel, "sourceCount" | "activeSourceCount">): SourceBadge {
  if (model.sourceCount === 0) return "none";
  if (model.activeSourceCount === 0) return "notEnabled";
  return "ok";
}

// --- status toggle --------------------------------------------------------------------------------

/** Optimistic row for a status toggle: disabling empties the active count. */
export function withStatus(model: PlatformModel, status: string): PlatformModel {
  return { ...model, status, activeSourceCount: status === "active" ? model.activeSourceCount : 0 };
}

/** True when the new status no longer matches the active status filter, so the row leaves the list. */
export function leavesList(filterStatus: string, newStatus: string): boolean {
  return filterStatus !== "" && filterStatus !== newStatus;
}

/** With this sort the order depends on the update time, so every toggle reloads. */
export function reloadsAfterToggle(sort: string): boolean {
  return sort === "updated_desc";
}

// --- bulk -------------------------------------------------------------------------------------------

/** Protocol changes only apply to models that have at least one source. */
export function protocolTargets(models: readonly PlatformModel[]): PlatformModel[] {
  return models.filter((m) => m.sourceCount > 0);
}

export function parsePositiveInt(text: string | number): number | null {
  const s = String(text).trim();
  if (!/^\d+$/.test(s)) return null;
  const n = Number(s);
  return Number.isSafeInteger(n) && n > 0 ? n : null;
}

/** Non-negative integer; anything invalid counts as 0. */
export function nonNegative(text: string | number): number {
  const s = String(text).trim();
  if (!/^\d+$/.test(s)) return 0;
  const n = Number(s);
  return Number.isSafeInteger(n) ? n : 0;
}

// --- model form --------------------------------------------------------------------------------

export interface ModelForm {
  name: string;
  vendor: string;
  /** 0 follows the vendor. */
  displayGroupID: number;
  kinds: string[];
  icon: string;
  capabilities: string;
  cbPolicyMode: "default" | "enforced";
  cbFailureThreshold: string;
  cbDurationMin: string;
  cbWindowMin: string;
  accessScope: "public" | "internal";
  status: "active" | "inactive";
  description: string;
  systemPrompt: string;
}

export function blankModelForm(): ModelForm {
  return {
    name: "",
    vendor: "unknown",
    displayGroupID: 0,
    kinds: [],
    icon: "",
    capabilities: "",
    cbPolicyMode: "default",
    cbFailureThreshold: "0",
    cbDurationMin: "0",
    cbWindowMin: "0",
    accessScope: "public",
    status: "active",
    description: "",
    systemPrompt: "",
  };
}

export function formOfModel(m: PlatformModel, kinds: string[]): ModelForm {
  const caps = m.capabilitiesJSON.trim();
  return {
    name: m.platformModelName,
    vendor: m.vendor || "unknown",
    displayGroupID: m.displayGroupID ?? 0,
    kinds,
    icon: m.icon,
    capabilities: caps === "{}" ? "" : m.capabilitiesJSON,
    cbPolicyMode: m.cbPolicyMode === "enforced" ? "enforced" : "default",
    cbFailureThreshold: String(m.cbFailureThreshold),
    cbDurationMin: String(m.cbDurationMin),
    cbWindowMin: String(m.cbWindowMin),
    accessScope: m.accessScope === "internal" ? "internal" : "public",
    status: m.status === "inactive" ? "inactive" : "active",
    description: m.description,
    systemPrompt: m.systemPrompt,
  };
}

export function validateModelForm(form: ModelForm): Problem | null {
  const name = form.name.trim();
  if (name.length < 2) return problem("adminModels.problems.name");
  if (name.length > 128) return problem("adminModels.problems.nameLong");
  return null;
}

function common(form: ModelForm, capabilitiesJSON: string): Record<string, unknown> {
  return {
    platformModelName: form.name.trim(),
    vendor: form.vendor.trim() || "unknown",
    icon: form.icon.trim(),
    capabilitiesJSON,
    systemPrompt: form.systemPrompt.trim(),
    accessScope: form.accessScope,
    status: form.status,
    description: form.description.trim(),
    cbPolicyMode: form.cbPolicyMode,
    cbFailureThreshold: nonNegative(form.cbFailureThreshold),
    cbDurationMin: nonNegative(form.cbDurationMin),
    cbWindowMin: nonNegative(form.cbWindowMin),
  };
}

/** On create the display group and kinds are left out when unset. */
export function createModelBody(form: ModelForm, capabilitiesJSON: string): Record<string, unknown> {
  const body = common(form, capabilitiesJSON);
  if (form.displayGroupID > 0) body.displayGroupID = form.displayGroupID;
  if (form.kinds.length > 0) body.kindsJSON = kindsJson(form.kinds);
  return body;
}

export function updateModelBody(form: ModelForm, capabilitiesJSON: string): Record<string, unknown> {
  return { ...common(form, capabilitiesJSON), displayGroupID: form.displayGroupID, kindsJSON: kindsJson(form.kinds) };
}

// --- initial sources -----------------------------------------------------------------------------

export interface SourceDraft {
  key: string;
  upstreamID: number;
  upstreamModelID: number;
  protocol: string;
  priority: string;
  weight: string;
}

export function blankSource(key: string): SourceDraft {
  return { key, upstreamID: 0, upstreamModelID: 0, protocol: "", priority: "1", weight: "1" };
}

/** A row with nothing chosen is ignored. */
export function hasSelection(row: SourceDraft): boolean {
  return row.upstreamID > 0 || row.upstreamModelID > 0 || row.protocol !== "";
}

export type SourceIssue = "upstream" | "model" | "protocol" | "priority" | "weight" | "duplicate";

export function validateSources(rows: readonly SourceDraft[]): { index: number; issue: SourceIssue } | null {
  const seen = new Set<string>();
  for (let index = 0; index < rows.length; index++) {
    const row = rows[index] as SourceDraft;
    if (!hasSelection(row)) continue;
    if (row.upstreamID <= 0) return { index, issue: "upstream" };
    if (row.upstreamModelID <= 0) return { index, issue: "model" };
    if (!row.protocol) return { index, issue: "protocol" };
    if (parsePositiveInt(row.priority) === null) return { index, issue: "priority" };
    if (parsePositiveInt(row.weight) === null) return { index, issue: "weight" };
    const id = `${row.upstreamID}/${row.upstreamModelID}/${row.protocol}`;
    if (seen.has(id)) return { index, issue: "duplicate" };
    seen.add(id);
  }
  return null;
}

export function sourceBody(row: SourceDraft): Record<string, unknown> {
  return {
    upstreamID: row.upstreamID,
    upstreamModelID: row.upstreamModelID,
    protocol: row.protocol,
    priority: parsePositiveInt(row.priority) ?? 1,
    weight: parsePositiveInt(row.weight) ?? 1,
    status: "active",
  };
}

export function sourcesToBind(rows: readonly SourceDraft[]): SourceDraft[] {
  return rows.filter(hasSelection);
}

// --- order ----------------------------------------------------------------------------------------

export interface OrderGroup {
  key: string;
  name: string;
  icon: string;
  models: PlatformModel[];
}

/** Models grouped by display group, otherwise vendor; groups appear where their first model does. */
export function groupForOrder(models: readonly PlatformModel[]): OrderGroup[] {
  const groups: OrderGroup[] = [];
  const index = new Map<string, OrderGroup>();
  for (const m of models) {
    const hasGroup = (m.displayGroupID ?? 0) > 0;
    const key = hasGroup ? `g:${m.displayGroupID}` : `v:${m.vendor || "unknown"}`;
    let g = index.get(key);
    if (!g) {
      g = hasGroup ? { key, name: m.displayGroupName, icon: m.displayGroupIcon, models: [] } : { key, name: m.vendorName || m.vendor, icon: m.vendorIcon, models: [] };
      index.set(key, g);
      groups.push(g);
    }
    g.models.push(m);
  }
  return groups;
}

export function flattenOrder(groups: readonly OrderGroup[]): number[] {
  return groups.flatMap((g) => g.models.map((m) => m.id));
}

// --- global circuit breaker ------------------------------------------------------------------------

export const BREAKER_KEY = "circuit_breaker.defaults";

/** Enabled iff the stored JSON object has `enabled === true`; anything else, including invalid JSON, is off. */
export function breakerEnabled(value: string | undefined | null): boolean {
  if (!value) return false;
  try {
    const parsed: unknown = JSON.parse(value);
    return isPlainObject(parsed) && parsed.enabled === true;
  } catch {
    return false;
  }
}

/** Writes only `enabled`; every other key is kept. Invalid stored JSON starts from an empty object. */
export function withBreaker(value: string | undefined | null, enabled: boolean): string {
  let base: Record<string, unknown> = {};
  try {
    const parsed: unknown = value ? JSON.parse(value) : {};
    if (isPlainObject(parsed)) base = parsed;
  } catch {
    base = {};
  }
  return JSON.stringify({ ...base, enabled });
}

// --- catalog sync outcome ------------------------------------------------------------------------------

export interface SyncSide {
  ok: boolean;
  count?: number;
  error?: string;
}

export type SyncOutcome = { kind: "success" } | { kind: "warning"; failed: "catalog" | "openrouter"; error: string } | { kind: "error"; errors: string[] };

/** Both ok is a success, one failing is a warning that names it, both failing is an error. */
export function syncOutcome(catalog: SyncSide, openrouter: SyncSide): SyncOutcome {
  if (catalog.ok && openrouter.ok) return { kind: "success" };
  if (!catalog.ok && !openrouter.ok) return { kind: "error", errors: [catalog.error ?? "", openrouter.error ?? ""].filter(Boolean) };
  return catalog.ok ? { kind: "warning", failed: "openrouter", error: openrouter.error ?? "" } : { kind: "warning", failed: "catalog", error: catalog.error ?? "" };
}

// --- presentation ------------------------------------------------------------------------------------------

const VENDOR_KEY = /^[a-z0-9][a-z0-9_-]{0,63}$/;

export function normalizeVendorKey(text: string): string {
  return text.trim().toLowerCase();
}

export function validateVendor(input: { key: string; name: string }, creating: boolean): "key" | "name" | null {
  if (creating && !VENDOR_KEY.test(normalizeVendorKey(input.key))) return "key";
  if (input.name.trim() === "") return "name";
  return null;
}

export interface InUse {
  count: number;
  names: string[];
  more: number;
}

/** What the server says about a vendor that is still referenced (details of `llm.model_vendor_in_use`). */
export function readVendorUsage(details: unknown): InUse {
  const d = isPlainObject(details) ? details : {};
  const models = Array.isArray(d.models) ? d.models.filter(isPlainObject).map((m) => (typeof m.platformModelName === "string" ? m.platformModelName : "")).filter(Boolean) : [];
  const count = typeof d.referenceCount === "number" ? d.referenceCount : models.length;
  return { count, names: models.slice(0, 3), more: Math.max(0, count - Math.min(3, models.length)) };
}

export function readIconUsage(details: unknown): number {
  const d = isPlainObject(details) ? details : {};
  return typeof d.referenceCount === "number" ? d.referenceCount : 0;
}

// --- icon upload ----------------------------------------------------------------------------------------------

export const ICON_TYPES = ["image/png", "image/jpeg", "image/webp"] as const;
export const ICON_MAX_BYTES = 1024 * 1024;

export function iconFileProblem(file: { type: string; size: number }): "type" | "size" | null {
  if (!(ICON_TYPES as readonly string[]).includes(file.type)) return "type";
  if (file.size <= 0 || file.size > ICON_MAX_BYTES) return "size";
  return null;
}

/** The assumed open circuit of a source that was just opened by the admin. */
export function guessCircuitUntil(now: number): string {
  return new Date(now + 24 * 3600 * 1000).toISOString();
}

/** Protocols a source of a model with these kinds may use; the current value is always kept. */
export function protocolChoices(kinds: readonly string[], current = ""): string[] {
  const wanted = new Set<string>();
  for (const k of kinds) for (const p of PROTOCOLS_BY_KIND[k as Kind] ?? []) wanted.add(p);
  const list = wanted.size === 0 ? [...PROTOCOLS] : [...wanted];
  if (current && !list.includes(current)) list.push(current);
  return sortProtocols(list);
}
