/**
 * Reads the free-form capabilities of a model (the admin-authored JSON) into a
 * typed shape: default options, option controls, locked paths, native tools
 * and the video-extension task.
 */
import { isRecord } from "@/lib/utils/guards";
import type { ModelDTO, ModelOptionPolicyDTO } from "../types";
import { parseKinds, type ModelFacts } from "./task-decision";
import { cloneJson, enableToolPayload, leafPaths, payloadSignature, type Options } from "./options";

export type OptionControlType = "boolean" | "number" | "select" | "text";

export interface OptionChoice {
  value: string;
  label: string;
}

export interface OptionControl {
  path: string;
  label: string;
  description: string;
  type: OptionControlType;
  options: OptionChoice[];
  placeholder: string;
  /** True when the control was inferred rather than declared. */
  inferred?: boolean;
}

export interface NativeToolOption {
  id: string;
  key: string;
  provider: string;
  type: string;
  label: string;
  description: string;
  defaultEnabled: boolean;
  payloads: Record<string, unknown>[];
}

export interface VideoExtensionCaps {
  available: boolean;
  defaultOptions: Options;
  optionControls: OptionControl[];
}

export interface ModelCaps {
  protocols: string[];
  defaultOptions: Options;
  optionControls: OptionControl[];
  lockedPaths: string[];
  nativeTools: NativeToolOption[];
  videoExtension: VideoExtensionCaps;
}

/** Choice lists for well-known parameter paths of media models. */
export const KNOWN_CHOICES: Record<string, string[]> = {
  size: ["auto", "1024x1024", "1536x1024", "1024x1536", "512x512", "256x256"],
  quality: ["auto", "low", "medium", "high", "standard", "hd"],
  aspect_ratio: ["1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "21:9"],
  aspectRatio: ["1:1", "16:9", "9:16", "4:3", "3:4", "3:2", "2:3", "21:9"],
  output_format: ["png", "jpeg", "webp"],
  background: ["auto", "transparent", "opaque"],
  moderation: ["auto", "low"],
  resolution: ["480p", "720p", "1080p", "1k", "2k", "4k"],
  image_size: ["1K", "2K", "4K"],
  imageSize: ["1K", "2K", "4K"],
  style: ["vivid", "natural"],
  input_fidelity: ["low", "high"],
  response_format: ["url", "b64_json"],
};

function pathTail(path: string): string {
  const i = path.lastIndexOf(".");
  return i < 0 ? path : path.slice(i + 1);
}

function strList(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((x): x is string => typeof x === "string" && x.trim() !== "").map((x) => x.trim()) : [];
}

export function parseProtocols(json: string | null | undefined): string[] {
  if (!json) return [];
  try {
    return strList(JSON.parse(json));
  } catch {
    return [];
  }
}

function parseChoices(raw: unknown): OptionChoice[] {
  if (!Array.isArray(raw)) return [];
  const out: OptionChoice[] = [];
  for (const item of raw) {
    if (typeof item === "string" || typeof item === "number") out.push({ value: String(item), label: String(item) });
    else if (isRecord(item) && (typeof item.value === "string" || typeof item.value === "number")) {
      out.push({ value: String(item.value), label: typeof item.label === "string" && item.label ? item.label : String(item.value) });
    }
  }
  return out;
}

export function parseOptionControls(raw: unknown): OptionControl[] {
  if (!Array.isArray(raw)) return [];
  const seen = new Set<string>();
  const out: OptionControl[] = [];
  for (const item of raw) {
    if (!isRecord(item) || typeof item.path !== "string" || !item.path.trim()) continue;
    const path = item.path.trim();
    if (seen.has(path)) continue;
    const type: OptionControlType = item.type === "boolean" || item.type === "number" || item.type === "select" ? item.type : "text";
    const options = parseChoices(item.options);
    if (type === "select" && options.length === 0) continue;
    seen.add(path);
    out.push({
      path,
      label: typeof item.label === "string" && item.label ? item.label : path,
      description: typeof item.description === "string" ? item.description : "",
      type,
      options,
      placeholder: typeof item.placeholder === "string" ? item.placeholder : "",
    });
  }
  return out;
}

export interface PolicyToolEntry {
  protocol: string;
  provider: string;
  type: string;
  toolKey: string;
  label: string;
  description: string;
  payload: Record<string, unknown>;
  defaultEnabled: boolean;
}

export function policyTools(policy: ModelOptionPolicyDTO | null | undefined): PolicyToolEntry[] {
  const raw = policy?.nativeTools;
  if (!Array.isArray(raw)) return [];
  const out: PolicyToolEntry[] = [];
  for (const item of raw) {
    if (!isRecord(item) || typeof item.toolKey !== "string") continue;
    out.push({
      protocol: String(item.protocol ?? ""),
      provider: String(item.provider ?? ""),
      type: String(item.type ?? ""),
      toolKey: item.toolKey,
      label: String(item.label ?? item.toolKey),
      description: String(item.description ?? ""),
      payload: isRecord(item.payload) ? item.payload : {},
      defaultEnabled: item.defaultEnabled === true,
    });
  }
  return out;
}

/** Merges tool entries (declared + catalog-derived) across protocol variants. */
function mergeNativeTools(entries: Omit<NativeToolOption, "id">[]): NativeToolOption[] {
  const map = new Map<string, NativeToolOption>();
  for (const e of entries) {
    const id = `${e.provider}::${e.key}`;
    const hit = map.get(id);
    if (!hit) {
      map.set(id, { ...e, id, payloads: [...e.payloads] });
      continue;
    }
    const known = new Set(hit.payloads.map(payloadSignature));
    for (const p of e.payloads) if (!known.has(payloadSignature(p))) hit.payloads.push(p);
    hit.defaultEnabled = hit.defaultEnabled || e.defaultEnabled;
  }
  return [...map.values()];
}

function parseNativeTools(caps: Record<string, unknown>, protocols: string[], catalog: PolicyToolEntry[]): NativeToolOption[] {
  const entries: Omit<NativeToolOption, "id">[] = [];
  const declared = Array.isArray(caps.nativeTools) ? caps.nativeTools : [];
  for (const item of declared) {
    if (!isRecord(item)) continue;
    if (item.enabled === false) continue;
    const key = String(item.id ?? item.key ?? item.toolKey ?? "").trim();
    if (!key) continue;
    const payload = isRecord(item.payload) ? item.payload : null;
    const fromCatalog = catalog.find((c) => c.toolKey === key && (!item.protocol || c.protocol === item.protocol));
    const resolved = payload ?? fromCatalog?.payload;
    if (!resolved) continue;
    entries.push({
      key,
      provider: String(item.provider ?? fromCatalog?.provider ?? ""),
      type: String(item.type ?? fromCatalog?.type ?? key),
      label: String(item.label ?? fromCatalog?.label ?? key),
      description: String(item.description ?? fromCatalog?.description ?? ""),
      defaultEnabled: item.defaultEnabled === true,
      payloads: [resolved],
    });
  }
  for (const key of strList(caps.nativeToolKeys)) {
    for (const c of catalog) {
      if (c.toolKey !== key) continue;
      if (protocols.length > 0 && !protocols.includes(c.protocol)) continue;
      entries.push({
        key,
        provider: c.provider,
        type: c.type || key,
        label: c.label,
        description: c.description,
        defaultEnabled: c.defaultEnabled,
        payloads: [c.payload],
      });
    }
  }
  return mergeNativeTools(entries);
}

export const DURATION_CONTROL: OptionControl = {
  path: "duration",
  label: "duration",
  description: "",
  type: "select",
  options: [2, 3, 4, 5, 6, 7, 8, 9, 10].map((n) => ({ value: String(n), label: `${n}s` })),
  placeholder: "",
};

function parseVideoExtension(caps: Record<string, unknown>, protocols: string[]): VideoExtensionCaps {
  const tasks = isRecord(caps.mediaTasks) ? caps.mediaTasks : {};
  const ext = isRecord(tasks.video_extension) ? tasks.video_extension : {};
  const available = protocols.includes("xai_video_extensions") && ext.enabled !== false;
  const defaultOptions = isRecord(ext.defaultOptions) ? cloneJson(ext.defaultOptions) : { duration: 6 };
  const declared = parseOptionControls(ext.optionControls);
  return { available, defaultOptions, optionControls: declared.length > 0 ? declared : [DURATION_CONTROL] };
}

export function parseModelCaps(model: ModelDTO, policy?: ModelOptionPolicyDTO | null): ModelCaps {
  let caps: Record<string, unknown> = {};
  try {
    const parsed: unknown = JSON.parse(model.capabilitiesJSON || "{}");
    if (isRecord(parsed)) caps = parsed;
  } catch {
    /* unreadable capabilities behave as none */
  }
  const protocols = parseProtocols(model.protocolsJSON);
  const nativeTools = parseNativeTools(caps, protocols, policyTools(policy));
  return {
    protocols,
    defaultOptions: isRecord(caps.defaultOptions) ? cloneJson(caps.defaultOptions) : {},
    optionControls: parseOptionControls(caps.optionControls),
    lockedPaths: strList(caps.lockedOptionPaths),
    nativeTools,
    videoExtension: parseVideoExtension(caps, protocols),
  };
}

export function modelFacts(model: ModelDTO | null | undefined, caps: ModelCaps | null): ModelFacts {
  return {
    kinds: parseKinds(model?.kindsJSON),
    supportsVideoExtension: caps?.videoExtension.available ?? false,
  };
}

/** Default options of a model with default-enabled native tools merged in. */
export function modelDefaultOptions(caps: ModelCaps): Options {
  let options: Options = cloneJson(caps.defaultOptions);
  for (const tool of caps.nativeTools) {
    if (!tool.defaultEnabled) continue;
    for (const payload of tool.payloads) options = enableToolPayload(options, payload);
  }
  return options;
}

// --- option policy -----------------------------------------------------------------

type PathDoc = Record<string, string[]>;

function parseDoc(json: string | undefined): PathDoc {
  if (!json) return {};
  try {
    const parsed: unknown = JSON.parse(json);
    if (!isRecord(parsed)) return {};
    const out: PathDoc = {};
    for (const [k, v] of Object.entries(parsed)) out[k.trim()] = strList(v);
    return out;
  } catch {
    return {};
  }
}

export type PathStatus = "passes" | "ignored" | "route" | "locked" | "notEnabled";

function listed(doc: PathDoc, protocols: string[], path: string): { all: boolean; some: boolean } {
  const keys = ["default", ...protocols];
  const hits = keys.map((k) => (doc[k] ?? []).some((p) => path === p || path.startsWith(`${p}.`)));
  const effective = protocols.length > 0 ? protocols.map((p) => hits[keys.indexOf(p)] === true || hits[0] === true) : [hits[0] === true];
  return { all: effective.every(Boolean), some: effective.some(Boolean) };
}

/** How the pass-through policy treats a parameter path for a model's protocols. */
export function pathPolicyStatus(policy: ModelOptionPolicyDTO | null | undefined, protocols: string[], path: string): "passes" | "ignored" | "route" {
  if (!policy || policy.mode === "disabled") return policy ? "ignored" : "passes";
  const denied = listed(parseDoc(policy.deniedPathsJSON), protocols, path);
  if (denied.all) return "ignored";
  const allowed = listed(parseDoc(policy.allowedPathsJSON), protocols, path);
  if (policy.mode === "denylist") return denied.some ? "route" : "passes";
  if (!allowed.some) return "ignored";
  if (!allowed.all || denied.some) return "route";
  return "passes";
}

export function pathAllowed(policy: ModelOptionPolicyDTO | null | undefined, protocols: string[], path: string): boolean {
  return pathPolicyStatus(policy, protocols, path) !== "ignored";
}

/**
 * Controls shown for a media model: the declared ones plus those inferred from
 * the model's default option leaves, limited by the pass-through policy.
 */
export function mediaControls(caps: ModelCaps, policy: ModelOptionPolicyDTO | null | undefined, source: "chat" | "extension" = "chat"): OptionControl[] {
  if (source === "extension") return caps.videoExtension.optionControls;
  const declared = caps.optionControls;
  const known = new Set(declared.map((c) => c.path));
  const out = [...declared];
  for (const { path, value } of leafPaths(caps.defaultOptions)) {
    if (known.has(path) || path === "tools" || path.startsWith("tools.")) continue;
    if (!pathAllowed(policy, caps.protocols, path)) continue;
    const tail = pathTail(path);
    if (typeof value === "boolean") {
      out.push({ path, label: path, description: "", type: "boolean", options: [], placeholder: "", inferred: true });
    } else if (typeof value === "number") {
      const choices = KNOWN_CHOICES[tail];
      if (choices && choices.every((c) => !Number.isNaN(Number(c)))) {
        out.push({ path, label: path, description: "", type: "select", options: choices.map((c) => ({ value: c, label: c })), placeholder: "", inferred: true });
      } else {
        out.push({ path, label: path, description: "", type: "number", options: [], placeholder: "", inferred: true });
      }
    } else if (typeof value === "string") {
      const choices = KNOWN_CHOICES[tail];
      if (choices) {
        const list = choices.includes(value) ? choices : [value, ...choices];
        out.push({ path, label: path, description: "", type: "select", options: list.map((c) => ({ value: c, label: c })), placeholder: "", inferred: true });
      } else {
        out.push({ path, label: path, description: "", type: "text", options: [], placeholder: "", inferred: true });
      }
    }
  }
  return out;
}

/** Sends numbers as numbers when the model's own default is numeric. */
export function coerceControlValue(control: OptionControl, raw: string, currentDefault: unknown): unknown {
  if (control.type === "boolean") return raw === "true";
  if (control.type === "number") {
    const n = Number(raw);
    return Number.isFinite(n) ? n : currentDefault;
  }
  if (typeof currentDefault === "number") {
    const n = Number(raw);
    if (Number.isFinite(n) && raw.trim() !== "") return n;
  }
  return raw;
}

/** Short display label from a platform model name. */
export function modelLabel(name: string): string {
  const trimmed = name.trim();
  const slash = trimmed.lastIndexOf("/");
  return slash >= 0 && slash < trimmed.length - 1 ? trimmed.slice(slash + 1) : trimmed;
}
