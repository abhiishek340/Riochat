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
