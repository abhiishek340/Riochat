/**
 * Reference data (billing configuration, plans, models, prices, groups) is
 * fetched for many screens. Answers are shared for 30 seconds, requests in
 * flight are shared too, and any change to models or their order drops the
 * cache.
 */
import { api } from "@/lib/api/client";
import { ApiError } from "@/lib/api/errors";
import { loadAllPages, readPage, type PageResult } from "./paging";
import { readBillingConfig, type BillingConfig, type BillingPlan, type ModelPrice, type PermissionGroup, type PlatformModel } from "./wire";

export const REFERENCE_TTL_MS = 30_000;

interface Entry {
  at: number;
  promise: Promise<unknown>;
}

const store = new Map<string, Entry>();

export function cachedLoad<T>(key: string, loader: () => Promise<T>, ttl: number = REFERENCE_TTL_MS, now: () => number = Date.now): Promise<T> {
  const hit = store.get(key);
  if (hit && now() - hit.at < ttl) return hit.promise as Promise<T>;
  const promise = loader();
  const entry: Entry = { at: now(), promise };
  store.set(key, entry);
  // A failed load must not be remembered.
  promise.catch(() => {
    if (store.get(key) === entry) store.delete(key);
  });
  return promise;
}

export function invalidateReference(prefix = ""): void {
  for (const key of [...store.keys()]) if (key.startsWith(prefix)) store.delete(key);
}

/** Drops everything a change to models or their order can affect. */
export function invalidateModels(): void {
  invalidateReference("models");
  invalidateReference("prices");
  invalidateReference("groups");
}

export function loadBillingConfig(): Promise<BillingConfig> {
  return cachedLoad("billing-config", async () => {
    const out = await api.get<{ config?: unknown }>("/admin/billing/config");
    return readBillingConfig(out?.config);
  });
}

export function isFeatureDisabled(error: unknown): boolean {
  return error instanceof ApiError && error.errorCode === "feature.disabled";
}

/** Plans for reference: a feature-disabled answer is simply "no plans". */
export function loadPlans(): Promise<BillingPlan[]> {
  return cachedLoad("billing-plans", async () => {
    try {
      const out = await api.get<BillingPlan[] | { results?: BillingPlan[] } | null>("/admin/billing/plans");
      if (Array.isArray(out)) return out;
      return out?.results ?? [];
    } catch (error) {
      if (isFeatureDisabled(error)) return [];
      throw error;
    }
  });
}

export function loadAllModels(): Promise<PlatformModel[]> {
  return cachedLoad("models-all", () =>
    loadAllPages<PlatformModel>(async (page, size): Promise<PageResult<PlatformModel>> =>
      readPage<PlatformModel>(await api.get<unknown>("/admin/llm/models", { query: { page, page_size: size, sort: "sortOrder_asc" } })),
    ),
  );
}

export function loadAllModelPrices(): Promise<ModelPrice[]> {
  return cachedLoad("prices-all", () =>
    loadAllPages<ModelPrice>(async (page, size) => readPage<ModelPrice>(await api.get<unknown>("/admin/billing/model-prices", { query: { page, page_size: size } }))),
  );
}

export function loadPermissionGroups(): Promise<PermissionGroup[]> {
  return cachedLoad("groups-all", async () => {
    const out = await api.get<{ results?: PermissionGroup[] } | null>("/admin/permission-groups");
    return out?.results ?? [];
  });
}
