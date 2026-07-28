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
