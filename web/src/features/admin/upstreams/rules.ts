/** Pure rules of the upstream screens: the form, key handling and route bindings. */
import { isPlainObject } from "../lib/json";
import { problem, type Problem } from "../lib/problem";
import { COMPATIBLE, KINDS, kindsForProtocols, kindsJson, parseKinds, PROTOCOLS_BY_KIND, sortProtocols, type Kind } from "../lib/protocols";
import type { Upstream, RouteRow } from "../llm-api";

// --- the upstream form ---------------------------------------------------------------

export type ProtocolDefaults = Partial<Record<Kind, string>>;

export interface UpstreamForm {
  name: string;
  baseURL: string;
  /** Create: one key per line, required. Edit: keys to add. */
  keys: string;
  compatible: string;
  status: string;
  defaults: ProtocolDefaults;
  connectTimeout: string;
  readTimeout: string;
  streamIdle: string;
  cbFailure: string;
  cbModel: string;
  cbLogic: string;
  cbDuration: string;
  cbWindow: string;
  headers: string;
}

export function blankForm(): UpstreamForm {
  return { name: "", baseURL: "", keys: "", compatible: "openai", status: "active", defaults: {}, connectTimeout: "", readTimeout: "", streamIdle: "", cbFailure: "", cbModel: "", cbLogic: "or", cbDuration: "", cbWindow: "", headers: "" };
}

export function parseDefaults(text: string | null | undefined): ProtocolDefaults {
  if (!text) return {};
  try {
    const value: unknown = JSON.parse(text);
    if (!isPlainObject(value)) return {};
    const out: ProtocolDefaults = {};
    for (const kind of KINDS) {
      const v = value[kind];
      if (typeof v === "string" && v) out[kind] = v;
    }
    return out;
  } catch {
    return {};
  }
}

/** Kind to protocol; an empty string when none is chosen. */
export function defaultsJson(defaults: ProtocolDefaults): string {
  const entries = KINDS.filter((k) => defaults[k]).map((k) => [k, defaults[k]] as const);
  return entries.length === 0 ? "" : JSON.stringify(Object.fromEntries(entries));
}

export function defaultCount(text: string | null | undefined): number {
  return Object.keys(parseDefaults(text)).length;
}

/** Protocols that can be the default for a kind. */
export function protocolsForKind(kind: Kind): readonly string[] {
  return PROTOCOLS_BY_KIND[kind];
}

export function formOf(u: Upstream): UpstreamForm {
  const n = (v: number) => (v > 0 ? String(v) : "");
  return {
    name: u.name,
    baseURL: u.baseURL,
    keys: "",
    compatible: u.compatible || "openai",
    status: u.status,
    defaults: parseDefaults(u.protocolDefaultsJSON),
    connectTimeout: n(u.connectTimeoutMS),
    readTimeout: n(u.readTimeoutMS),
    streamIdle: n(u.streamIdleTimeoutMS),
    cbFailure: n(u.cbFailureThreshold),
    cbModel: n(u.cbModelThreshold),
    cbLogic: u.cbThresholdLogic === "and" ? "and" : "or",
    cbDuration: n(u.cbDurationMin),
    cbWindow: n(u.cbWindowMin),
    headers: u.headersJSON && u.headersJSON !== "{}" ? u.headersJSON : "",
  };
}

/** One key per line, trimmed, blanks dropped. */
export function keyLines(text: string): string[] {
  return text
    .split(/\r?\n/)
    .map((l) => l.trim())
    .filter(Boolean);
}

/** The JSON string the API takes for new keys. */
export function keysDocument(lines: readonly string[]): string {
  return JSON.stringify({ keys: lines.map((key) => ({ key, status: "active" })), strategy: "round_robin" });
}

function whole(text: string): number | null {
  const t = text.trim();
  if (t === "") return 0;
  return /^\d+$/.test(t) ? Number(t) : null;
}

export type FormProblem = "name" | "baseURL" | "keysRequired" | "number" | "headers" | "keysReplace";

export function validateForm(form: UpstreamForm, editing: boolean): FormProblem | null {
  if (form.name.trim().length < 2) return "name";
  if (!/^https?:\/\/\S+$/i.test(form.baseURL.trim())) return "baseURL";
  if (!editing && keyLines(form.keys).length === 0) return "keysRequired";
  for (const text of [form.connectTimeout, form.readTimeout, form.streamIdle, form.cbFailure, form.cbModel, form.cbDuration, form.cbWindow]) if (whole(text) === null) return "number";
  const headers = form.headers.trim();
  if (headers) {
    try {
      if (!isPlainObject(JSON.parse(headers))) return "headers";
    } catch {
      return "headers";
    }
  }
  return null;
}

/** Create body: blank numeric fields are left out. */
export function createBody(form: UpstreamForm): Record<string, unknown> {
  const body: Record<string, unknown> = {
    name: form.name.trim(),
    baseURL: form.baseURL.trim(),
    apiKeys: keysDocument(keyLines(form.keys)),
    compatible: form.compatible,
    status: form.status,
    cbThresholdLogic: form.cbLogic === "and" ? "and" : "or",
  };
  const defaults = defaultsJson(form.defaults);
  if (defaults) body.protocolDefaultsJSON = defaults;
  const numbers: Array<[string, string]> = [
    ["connectTimeoutMS", form.connectTimeout],
    ["readTimeoutMS", form.readTimeout],
    ["streamIdleTimeoutMS", form.streamIdle],
    ["cbFailureThreshold", form.cbFailure],
    ["cbModelThreshold", form.cbModel],
    ["cbDurationMin", form.cbDuration],
    ["cbWindowMin", form.cbWindow],
  ];
  for (const [key, text] of numbers) if (text.trim() !== "") body[key] = Number(text.trim());
  if (form.headers.trim()) body.headersJSON = form.headers.trim();
  return body;
}

/** Changing the compatibility clears the protocol defaults. */
export function withCompatible(form: UpstreamForm, compatible: string): UpstreamForm {
  return compatible === form.compatible ? form : { ...form, compatible, defaults: {} };
}

/** At least one key must stay usable: remaining active keys plus keys being added. */
export function keyProblem(upstream: Pick<Upstream, "apiKeyItems">, deleteIds: ReadonlySet<string>, addLines: readonly string[]): Problem | null {
  const remaining = upstream.apiKeyItems.filter((k) => !deleteIds.has(k.id) && (k.status === "" || k.status === "active")).length;
  return remaining + addLines.length > 0 ? null : problem("adminUpstreams.problems.replacementKey");
}

/** Update body: only changed fields; defaults travel again whenever the compatibility changed. */
export function updateBody(original: Upstream, form: UpstreamForm, deleteIds: ReadonlySet<string>): Record<string, unknown> {
  const body: Record<string, unknown> = {};
  const base = formOf(original);
  if (form.name.trim() !== original.name) body.name = form.name.trim();
  if (form.baseURL.trim() !== original.baseURL) body.baseURL = form.baseURL.trim();
  const compatChanged = form.compatible !== base.compatible;
  if (compatChanged) body.compatible = form.compatible;
  if (compatChanged || defaultsJson(form.defaults) !== defaultsJson(base.defaults)) body.protocolDefaultsJSON = defaultsJson(form.defaults);
  if (form.status !== original.status) body.status = form.status;
  const numbers: Array<[string, keyof UpstreamForm, number]> = [
    ["connectTimeoutMS", "connectTimeout", original.connectTimeoutMS],
    ["readTimeoutMS", "readTimeout", original.readTimeoutMS],
    ["streamIdleTimeoutMS", "streamIdle", original.streamIdleTimeoutMS],
    ["cbFailureThreshold", "cbFailure", original.cbFailureThreshold],
    ["cbModelThreshold", "cbModel", original.cbModelThreshold],
    ["cbDurationMin", "cbDuration", original.cbDurationMin],
    ["cbWindowMin", "cbWindow", original.cbWindowMin],
  ];
  for (const [key, field, before] of numbers) {
    const now = whole(form[field] as string) ?? before;
    if (now !== before) body[key] = now;
  }
  if (form.cbLogic !== base.cbLogic) body.cbThresholdLogic = form.cbLogic;
  if (form.headers.trim() !== base.headers.trim()) body.headersJSON = form.headers.trim() || "{}";
  const add = keyLines(form.keys);
  if (add.length > 0) body.addAPIKeys = keysDocument(add);
  if (deleteIds.size > 0) body.deleteAPIKeyIDs = [...deleteIds].sort();
  return body;
}

export function isCompatible(value: string): boolean {
  return (COMPATIBLE as readonly string[]).includes(value);
}

/** An inactive upstream, or one with its circuit open, serves no models. */
export function activeModelsShown(u: Pick<Upstream, "status" | "circuitOpen" | "activeModelsCount">): number {
  return u.status !== "active" || u.circuitOpen ? 0 : u.activeModelsCount;
}

// --- route bindings ---------------------------------------------------------------------

export interface BindingRow {
  /** Stable key: `${upstreamModelName}\u0000${platformModelName}` of the original data. */
  key: string;
  /** Lowest route id of the pair, or 0 for a model with no route yet. */
  id: number;
  routeIds: number[];
  upstreamModelName: string;
  platformModelName: string;
  protocols: string[];
  kinds: Kind[];
  routeStatus: "active" | "inactive";
  upstreamModelStatus: string;
  /** The row's route status was changed by the admin. */
  statusTouched: boolean;
  circuitOpen: boolean;
  original: { platformModelName: string; protocols: string[]; kinds: Kind[]; routeStatus: "active" | "inactive" };
}

/**
 * One row per (upstream model, platform model) pair. Routes with several
 * protocols for one pair collapse into a row; the row is active when any
 * member is, and its id is the lowest route id.
 */
export function groupRoutes(routes: readonly RouteRow[]): BindingRow[] {
  const map = new Map<string, BindingRow>();
  for (const r of routes) {
    const key = `${r.upstreamModelName}\u0000${r.platformModelName}`;
    const protocols = r.protocol ? [r.protocol] : [];
    const kinds = parseKinds(r.modelKindsJSON || r.upstreamModelKindsJSON);
    const existing = map.get(key);
    const routeId = r.routeID || r.id;
    if (!existing) {
      const row: BindingRow = {
        key,
        id: routeId,
        routeIds: r.routeID ? [r.routeID] : [],
        upstreamModelName: r.upstreamModelName,
        platformModelName: r.platformModelName,
        protocols: sortProtocols(protocols),
        kinds,
        routeStatus: r.routeStatus === "active" ? "active" : "inactive",
        upstreamModelStatus: r.upstreamModelStatus,
        statusTouched: false,
        circuitOpen: r.circuitOpen,
        original: { platformModelName: r.platformModelName, protocols: sortProtocols(protocols), kinds, routeStatus: r.routeStatus === "active" ? "active" : "inactive" },
      };
      map.set(key, row);
      continue;
    }
    if (r.routeID) existing.routeIds.push(r.routeID);
    existing.id = Math.min(existing.id || routeId, routeId || existing.id);
    existing.protocols = sortProtocols([...existing.protocols, ...protocols]);
    if (r.routeStatus === "active") existing.routeStatus = "active";
    if (r.circuitOpen) existing.circuitOpen = true;
    existing.original = { platformModelName: existing.platformModelName, protocols: existing.protocols, kinds: existing.kinds, routeStatus: existing.routeStatus };
  }
  return [...map.values()];
}

export function kindsOfRow(row: Pick<BindingRow, "protocols" | "kinds">): Kind[] {
  return row.protocols.length > 0 ? kindsForProtocols(row.protocols) : row.kinds.length > 0 ? row.kinds : ["chat"];
}

export function rowDirty(row: BindingRow): boolean {
  const o = row.original;
  return (
    row.platformModelName.trim() !== o.platformModelName.trim() ||
    row.routeStatus !== o.routeStatus ||
    row.protocols.join(",") !== o.protocols.join(",") ||
    kindsOfRow(row).join(",") !== (o.kinds.length > 0 ? o.kinds : (["chat"] as Kind[])).join(",")
  );
}

export type BindingProblem = { kind: "upstreamModel" | "platformModel" | "duplicate"; row: number };

/** Checks in order: names, active routes need a platform model, no repeated triple. */
export function validateBindings(rows: readonly BindingRow[]): BindingProblem | null {
  for (let i = 0; i < rows.length; i++) if (!(rows[i] as BindingRow).upstreamModelName.trim()) return { kind: "upstreamModel", row: i };
  for (let i = 0; i < rows.length; i++) {
    const r = rows[i] as BindingRow;
    if (r.routeStatus === "active" && !r.platformModelName.trim()) return { kind: "platformModel", row: i };
  }
  const seen = new Map<string, number>();
  for (let i = 0; i < rows.length; i++) {
    const r = rows[i] as BindingRow;
    const platform = r.platformModelName.trim();
    if (!platform) continue;
    for (const protocol of r.protocols) {
      const triple = `${r.upstreamModelName.trim()}\u0000${platform}\u0000${protocol}`;
      if (seen.has(triple)) return { kind: "duplicate", row: i };
      seen.set(triple, i);
    }
  }
  return null;
}

export interface SavePlan {
  upserts: Array<{ upstreamModelName: string; platformModelName: string; kindsJSON: string; protocols: string[]; routeIDs: number[]; status?: string }>;
  deletes: number[];
}

/**
 * Upserts first, deletes after. A row with routes that is inactive and has no
 * platform model is deleted route by route; any row with a platform model is
 * upserted. The status travels only when the admin touched it.
 */
export function planSave(rows: readonly BindingRow[]): SavePlan {
  const plan: SavePlan = { upserts: [], deletes: [] };
  for (const row of rows) {
    if (!rowDirty(row)) continue;
    const platform = row.platformModelName.trim();
    if (!platform) {
      if (row.routeIds.length > 0 && row.routeStatus === "inactive") plan.deletes.push(...row.routeIds);
      continue;
    }
    const upsert: SavePlan["upserts"][number] = {
      upstreamModelName: row.upstreamModelName.trim(),
      platformModelName: platform,
      kindsJSON: kindsJson(kindsOfRow(row)),
      protocols: row.protocols,
      routeIDs: row.routeIds,
    };
    if (row.statusTouched) upsert.status = row.routeStatus;
    plan.upserts.push(upsert);
  }
  return plan;
}

/** New binding: both names are required; the rest has defaults. */
export function newBindingProblem(upstreamModel: string, platformModel: string): "upstreamModel" | "platformModel" | null {
  if (!upstreamModel.trim()) return "upstreamModel";
  if (!platformModel.trim()) return "platformModel";
  return null;
}
