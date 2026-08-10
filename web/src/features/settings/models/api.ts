import { api, } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { isRecord, num, str } from "@/lib/utils/guards";

export type ProviderStatus = "active" | "disabled" | "suspended";
export type ProviderSource = "manual" | "link";

export interface ProviderAccess {
  enabled: boolean;
  maxPerUser: number;
  protocols: string[];
  modelProtocols: string[];
}

export interface ProviderModel {
  name: string;
  protocols: string[];
}

export interface PersonalProvider {
  id: string;
  name: string;
  icon: string;
  protocol: string;
  baseURL: string;
  host: string;
  keyHint: string;
  models: ProviderModel[];
  status: ProviderStatus;
  source: ProviderSource;
  lastError: string;
  lastCheckedAt: string | null;
  createdAt: string;
  updatedAt: string;
}

export interface AvailableModel {
  name: string;
  suggestedProtocols: string[];
}

/** What the create/update calls accept for one model. */
export type ModelInput = string | ProviderModel;

function strings(value: unknown): string[] {
  return Array.isArray(value) ? value.filter((v): v is string => typeof v === "string" && v.trim() !== "") : [];
}

/**
 * Older servers answer with plain model names and no protocols: convert both
 * shapes to `{name, protocols}` and drop blank names.
 */
export function normalizeModels(raw: unknown, fallbackProtocol: string): ProviderModel[] {
  if (!Array.isArray(raw)) return [];
  const out: ProviderModel[] = [];
  for (const item of raw) {
    if (typeof item === "string") {
      const name = item.trim();
      if (name) out.push({ name, protocols: fallbackProtocol ? [fallbackProtocol] : [] });
    } else if (isRecord(item)) {
      const name = str(item.name).trim();
      if (!name) continue;
      const protocols = strings(item.protocols);
      out.push({ name, protocols: protocols.length ? protocols : fallbackProtocol ? [fallbackProtocol] : [] });
    }
  }
  return out;
}

export function normalizeAvailable(raw: unknown, fallbackProtocol: string): AvailableModel[] {
  if (!Array.isArray(raw)) return [];
  const out: AvailableModel[] = [];
  for (const item of raw) {
    if (typeof item === "string") {
      const name = item.trim();
      if (name) out.push({ name, suggestedProtocols: fallbackProtocol ? [fallbackProtocol] : [] });
    } else if (isRecord(item)) {
      const name = str(item.name).trim();
      if (!name) continue;
      const suggested = strings(item.suggestedProtocols);
      out.push({ name, suggestedProtocols: suggested.length ? suggested : fallbackProtocol ? [fallbackProtocol] : [] });
    }
  }
  return out;
}

export function normalizeProvider(raw: unknown): PersonalProvider {
  const r = isRecord(raw) ? raw : {};
  const protocol = str(r.protocol);
  const status = str(r.status);
  return {
    id: str(r.id),
    name: str(r.name),
    icon: str(r.icon),
    protocol,
    baseURL: str(r.baseURL),
    host: str(r.host),
    keyHint: str(r.keyHint),
    models: normalizeModels(r.models, protocol),
    status: status === "disabled" || status === "suspended" ? status : "active",
    source: r.source === "link" ? "link" : "manual",
    lastError: str(r.lastError),
    lastCheckedAt: typeof r.lastCheckedAt === "string" ? r.lastCheckedAt : null,
    createdAt: str(r.createdAt),
    updatedAt: str(r.updatedAt),
  };
}

export function normalizeAccess(raw: unknown): ProviderAccess {
  const r = isRecord(raw) ? raw : {};
  return {
    enabled: r.enabled === true,
    maxPerUser: Math.max(0, num(r.maxPerUser, 0)),
    protocols: strings(r.protocols),
    modelProtocols: strings(r.modelProtocols),
  };
}

const BASE = "/me/model-providers";

export async function fetchAccess(signal?: AbortSignal): Promise<ProviderAccess> {
  return normalizeAccess(await api.get<unknown>(`${BASE}/access`, { signal }));
}

export async function listProviders(signal?: AbortSignal): Promise<PersonalProvider[]> {
  const data = await api.get<{ providers?: unknown[] }>(BASE, { signal });
  return (data.providers ?? []).map(normalizeProvider);
}

export interface ProbeInput {
  protocol: string;
  baseURL: string;
  apiKey: string;
}

export async function probeProvider(input: ProbeInput, signal?: AbortSignal): Promise<AvailableModel[]> {
  const data = await api.post<{ models?: unknown }>(`${BASE}/probe`, input, { signal });
  return normalizeAvailable(data.models, input.protocol);
}

export interface CreateInput {
  name?: string;
  icon?: string;
  protocol: string;
  baseURL: string;
  apiKey: string;
  models: ModelInput[];
  source: ProviderSource;
}

export async function createProvider(input: CreateInput): Promise<PersonalProvider> {
  const data = await api.post<{ provider: unknown }>(BASE, input);
  return normalizeProvider(data.provider);
}

export interface UpdateInput {
  name?: string;
  icon?: string;
  apiKey?: string;
  models?: ModelInput[];
  enabled?: boolean;
}

export async function updateProvider(id: string, input: UpdateInput): Promise<PersonalProvider> {
  const data = await api.patch<{ provider: unknown }>(`${BASE}/${seg(id)}`, input);
  return normalizeProvider(data.provider);
}

export async function deleteProvider(id: string): Promise<void> {
  await api.delete(`${BASE}/${seg(id)}`);
}

export async function fetchProviderModels(id: string, protocol: string, signal?: AbortSignal): Promise<AvailableModel[]> {
  const data = await api.get<{ models?: unknown }>(`${BASE}/${seg(id)}/models`, { signal });
  return normalizeAvailable(data.models, protocol);
}
