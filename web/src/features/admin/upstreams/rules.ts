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
