"use client";

/**
 * Cached server data the chat page depends on: models, policies, settings,
 * tools, components. Each resource is a store with `ensure()` (load once) and
 * `refresh()` (load again); hooks load on mount.
 */
import { useEffect } from "react";
import {
  getBillingConfig,
  getFilePolicy,
  getMcpPolicy,
  getModelOptionPolicy,
  getUserSettings,
  listMcpTools,
  listModels,
  listUiComponents,
  patchUserSettings,
} from "../api";
import { parsePrefs, type ChatPrefs, DEFAULT_PREFS } from "../lib/settings";
import type { FilePolicyDTO, McpToolDTO, ModelDTO, ModelOptionPolicyDTO, UiComponentDTO } from "../types";
import { createStore, useStore, type Store } from "./store";

export interface ResourceState<T> {
  data: T;
  loading: boolean;
  loaded: boolean;
  error: unknown;
}

interface Resource<T> {
  store: Store<ResourceState<T>>;
  ensure: () => Promise<void>;
  refresh: () => Promise<void>;
  reset: () => void;
}

function createResource<T>(initial: T, fetcher: () => Promise<T>, merge?: (prev: T, next: T) => T): Resource<T> {
  const empty: ResourceState<T> = { data: initial, loading: false, loaded: false, error: null };
  const store = createStore<ResourceState<T>>(empty);
  let inflight: Promise<void> | null = null;
  const refresh = (): Promise<void> => {
    if (inflight) return inflight;
    store.set((s) => ({ ...s, loading: true }));
    inflight = fetcher()
      .then(
        (next) => store.set((s) => ({ data: merge ? merge(s.data, next) : next, loading: false, loaded: true, error: null })),
        (error: unknown) => store.set((s) => ({ ...s, loading: false, loaded: s.loaded, error })),
      )
      .finally(() => {
        inflight = null;
      });
    return inflight;
  };
  return {
    store,
    refresh,
    ensure: () => (store.get().loaded ? Promise.resolve() : refresh()),
    reset: () => store.set(empty),
  };
}

/** Keeps object identity of unchanged models so picker rows do not re-render. */
export function mergeModels(prev: ModelDTO[], next: ModelDTO[]): ModelDTO[] {
  const old = new Map(prev.map((m) => [m.platformModelName, m] as const));
  let same = prev.length === next.length;
  const out = next.map((m, i) => {
    const hit = old.get(m.platformModelName);
    const keep = hit && JSON.stringify(hit) === JSON.stringify(m) ? hit : m;
    if (keep !== prev[i]) same = false;
    return keep;
  });
  return same ? prev : out;
}

export const modelsResource = createResource<ModelDTO[]>([], listModels, mergeModels);
export const optionPolicyResource = createResource<ModelOptionPolicyDTO | null>(null, () => getModelOptionPolicy());
export const settingsResource = createResource<Record<string, string>>({}, () => getUserSettings());
export const billingResource = createResource<{ mode: string; displayCurrency: "USD" | "CNY"; usdToCNYRate: number }>(
  { mode: "self", displayCurrency: "USD", usdToCNYRate: 1 },
  async () => {
    const c = await getBillingConfig();
    return { mode: c.mode, displayCurrency: c.displayCurrency === "CNY" ? "CNY" : "USD", usdToCNYRate: c.usdToCNYRate };
  },
);
export const filePolicyResource = createResource<FilePolicyDTO | null>(null, () => getFilePolicy());
export const mcpToolsResource = createResource<McpToolDTO[]>([], async () => {
  const seen = new Set<number>();
  return (await listMcpTools()).filter((t) => {
    const ok = Number.isInteger(t.id) && t.id > 0 && !seen.has(t.id) && (t.status === "" || t.status === "active");
    if (ok) seen.add(t.id);
    return ok;
  });
});
export const mcpPolicyResource = createResource<{ maxSelectedToolsPerMessage: number }>({ maxSelectedToolsPerMessage: 32 }, () => getMcpPolicy());
export const uiComponentsResource = createResource<UiComponentFull[]>([], async () => (await listUiComponents()).results);

export type UiComponentFull = UiComponentDTO & { rendererSource?: string; propsSchema?: string; propsSummary?: string };

export function resetResources(): void {
  for (const r of [modelsResource, optionPolicyResource, settingsResource, billingResource, filePolicyResource, mcpToolsResource, mcpPolicyResource, uiComponentsResource]) r.reset();
}

function useResource<T>(r: Resource<T>, load = true): ResourceState<T> {
  const state = useStore(r.store);
  // A reset (user change) puts the store back to its empty state; loading again is up to the hooks that are still mounted.
  const idle = !state.loaded && !state.loading && state.error === null;
  useEffect(() => {
    if (load) void r.ensure();
  }, [r, load, idle]);
  return state;
}

export const useModelsState = () => useResource(modelsResource);
export const useOptionPolicy = () => useResource(optionPolicyResource).data;
export const useBilling = () => useResource(billingResource).data;
export const useFilePolicy = () => useResource(filePolicyResource).data;
export const useMcpTools = () => useResource(mcpToolsResource);
export const useMcpPolicy = () => useResource(mcpPolicyResource).data;
export const useUiComponents = () => useResource(uiComponentsResource);

/** Parsed chat settings; the raw map is available too. */
export function useChatPrefs(): ChatPrefs {
  const state = useResource(settingsResource);
  return state.loaded ? parsePrefs(state.data) : DEFAULT_PREFS;
}

export function useSettingsLoaded(): boolean {
  return useResource(settingsResource).loaded;
}

export function useSettingsMap(): Record<string, string> {
  return useResource(settingsResource).data;
}

/** Optimistic write of a few settings; the previous values return on failure. */
export async function saveSettings(patch: Record<string, string>): Promise<void> {
  const before = settingsResource.store.get().data;
  settingsResource.store.set((s) => ({ ...s, data: { ...s.data, ...patch } }));
  try {
    const saved = await patchUserSettings(patch);
    settingsResource.store.set((s) => ({ ...s, data: { ...s.data, ...saved }, loaded: true }));
  } catch (error) {
    settingsResource.store.set((s) => ({ ...s, data: before }));
    throw error;
  }
}
