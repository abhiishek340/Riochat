/** Calls of the Models page that are not part of the shared channel API. */
import { api } from "@/lib/api/client";
import { isPlainObject } from "../lib/json";
import { loadOfficial } from "../billing/api";
import { fetchSettings } from "../lib/use-settings-form";
import { findItem } from "../lib/settings-form";
import { catalogStatus, listLlmSettings, listRoutes, patchLlmSetting, refreshCatalog, type CatalogStatus, type LlmSetting } from "../llm-api";
import type { CatalogTool } from "./capabilities";
import { BREAKER_KEY, withBreaker, type SyncSide } from "./rules";

/** The server's native tool catalog (from the public model-option policy). */
export async function loadNativeToolCatalog(signal?: AbortSignal): Promise<CatalogTool[]> {
  const out = await api.get<{ nativeTools?: unknown[] } | null>("/settings/model-option-policy", { signal });
  const list = Array.isArray(out?.nativeTools) ? out.nativeTools : [];
  return list.flatMap((raw): CatalogTool[] => {
    if (!isPlainObject(raw) || typeof raw.toolKey !== "string" || typeof raw.protocol !== "string") return [];
    return [
      {
        toolKey: raw.toolKey,
        type: typeof raw.type === "string" ? raw.type : "",
        provider: typeof raw.provider === "string" ? raw.provider : "",
        label: typeof raw.label === "string" ? raw.label : raw.toolKey,
        description: typeof raw.description === "string" ? raw.description : "",
        protocol: raw.protocol,
        payload: raw.payload,
        defaultEnabled: raw.defaultEnabled === true,
      },
    ];
  });
}

export interface UpstreamModelOption {
  id: number;
  name: string;
  suggestedProtocol: string;
  status: string;
}

/** Models an upstream offers, one entry per upstream model. */
export async function loadUpstreamModels(upstreamId: number, q: string, signal?: AbortSignal): Promise<UpstreamModelOption[]> {
  const page = await listRoutes(upstreamId, { page: 1, pageSize: 100, q, routeStatus: "", upstreamStatus: "", protocol: "", sort: "platform_asc" }, signal);
  const seen = new Map<number, UpstreamModelOption>();
  for (const r of page.results) {
    if (!seen.has(r.id)) seen.set(r.id, { id: r.id, name: r.upstreamModelName, suggestedProtocol: r.suggestedProtocol, status: r.upstreamModelStatus });
  }
  return [...seen.values()];
}

export interface BreakerSetting {
  exists: boolean;
  value: string;
}

export async function loadBreaker(signal?: AbortSignal): Promise<BreakerSetting> {
  const list: LlmSetting[] = await listLlmSettings(signal);
  const row = list.find((s) => s.key === BREAKER_KEY);
  return { exists: row !== undefined, value: row?.value ?? "" };
}

/** Flips only `enabled`; the other keys of the stored object are kept. */
export async function saveBreaker(current: string, enabled: boolean): Promise<string> {
  const next = withBreaker(current, enabled);
  const saved = await patchLlmSetting(BREAKER_KEY, next);
  return saved.value;
}

export interface CatalogSync {
  catalog: SyncSide;
  openrouter: SyncSide;
  status: CatalogStatus | null;
}

const reason = (error: unknown) => (error instanceof Error ? error.message : String(error));

/** Refreshes both external catalogs at once; a stale OpenRouter answer counts as a failure. */
export async function syncCatalogs(): Promise<CatalogSync> {
  const [catalog, openrouter] = await Promise.allSettled([refreshCatalog(), loadOfficial(true)]);
  const status = catalog.status === "fulfilled" ? catalog.value : null;
  return {
    status,
    catalog: catalog.status === "fulfilled" ? (catalog.value.lastError ? { ok: false, error: catalog.value.lastError } : { ok: true, count: catalog.value.modelCount }) : { ok: false, error: reason(catalog.reason) },
    openrouter: openrouter.status === "fulfilled" ? (openrouter.value.stale ? { ok: false, error: "stale" } : { ok: true, count: openrouter.value.items.length }) : { ok: false, error: reason(openrouter.reason) },
  };
}

/** Opening the sheet asks for the catalog status, which lets the server refresh data older than a day. */
export function touchCatalog(signal?: AbortSignal): Promise<CatalogStatus> {
  return catalogStatus(signal);
}

/** The global fallback context window shown as the last resort for automatic mode. */
export async function loadFallbackWindow(signal?: AbortSignal): Promise<number> {
  try {
    const tree = await fetchSettings(signal);
    const n = Number(findItem(tree, "chat", "context_window_fallback_tokens")?.value);
    return Number.isInteger(n) && n > 0 ? n : 128_000;
  } catch {
    return 128_000;
  }
}
