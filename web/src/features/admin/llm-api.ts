/** Calls and wire shapes of the model channel API (/admin/llm/...). */
import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { readPage, type PageResult } from "./lib/paging";
import type { PermissionGroup, PlatformModel } from "./lib/wire";

const LLM = "/admin/llm";

// --- upstreams ------------------------------------------------------------------

export interface ApiKeyItem {
  id: string;
  index: number;
  keyMasked: string;
  note: string;
  status: string;
}

export interface Upstream {
  id: number;
  name: string;
  baseURL: string;
  compatible: string;
  protocolDefaultsJSON: string;
  apiKeysMasked: string;
  apiKeyItems: ApiKeyItem[];
  status: "active" | "inactive" | (string & {});
  connectTimeoutMS: number;
  readTimeoutMS: number;
  streamIdleTimeoutMS: number;
  cbFailureThreshold: number;
  cbModelThreshold: number;
  cbThresholdLogic: "or" | "and" | (string & {});
  cbDurationMin: number;
  cbWindowMin: number;
  headersJSON: string;
  modelsCount: number;
  activeModelsCount: number;
  circuitOpen: boolean;
  circuitUntil: string;
  createdAt: string;
  updatedAt: string;
}

export interface UpstreamQuery {
  page: number;
  pageSize: number;
  q: string;
  status: string;
  compatible: string;
  sort: string;
}

export async function listUpstreams(query: UpstreamQuery, signal?: AbortSignal): Promise<PageResult<Upstream>> {
  const raw = await api.get<unknown>(`${LLM}/upstreams`, { signal, query: { page: query.page, page_size: query.pageSize, q: query.q.trim(), status: query.status, compatible: query.compatible, sort: query.sort } });
  return readPage<Upstream>(raw);
}

export async function listAllUpstreams(signal?: AbortSignal): Promise<Upstream[]> {
  const raw = await api.get<unknown>(`${LLM}/upstreams`, { signal, query: { page: 1, page_size: 1000, sort: "name_asc" } });
  return readPage<Upstream>(raw).results;
}

export async function createUpstream(body: Record<string, unknown>): Promise<Upstream> {
  const out = await api.post<{ upstream: Upstream }>(`${LLM}/upstreams`, body);
  return out.upstream;
}

export async function updateUpstream(id: number, body: Record<string, unknown>): Promise<Upstream> {
  const out = await api.patch<{ upstream: Upstream }>(`${LLM}/upstreams/${seg(id)}`, body);
  return out.upstream;
}

export async function deleteUpstream(id: number): Promise<void> {
  await api.delete(`${LLM}/upstreams/${seg(id)}`);
}

export interface BatchResult {
  total: number;
  successCount: number;
  notFoundCount: number;
  failedCount: number;
  results: Array<{ id: number; status: string; error?: string }>;
}

export function batchDeleteUpstreams(ids: number[]): Promise<BatchResult> {
  return api.post<BatchResult>(`${LLM}/upstreams/batch-delete`, { ids });
}

export async function upstreamCircuit(id: number, action: "open" | "reset"): Promise<void> {
  await api.post(`${LLM}/upstreams/${seg(id)}/circuit/${action}`);
}

// --- routes (upstream models) ---------------------------------------------------------

export interface RouteRow {
  id: number;
  routeID: number;
  upstreamID: number;
  bindingCode: string;
  platformModelID: number;
  platformModelName: string;
  modelVendor: string;
  modelKindsJSON: string;
  modelIcon: string;
  upstreamModelName: string;
  upstreamModelVendor: string;
  upstreamModelIcon: string;
  upstreamModelKindsJSON: string;
  suggestedProtocol: string;
  protocol: string;
  upstreamModelStatus: string;
  routeStatus: string;
  priority: number;
  weight: number;
  source: string;
  cbFailureThreshold: number;
  cbDurationMin: number;
  cbWindowMin: number;
  headersJSON: string;
  circuitOpen: boolean;
  circuitUntil: string;
  createdAt: string;
  updatedAt: string;
}

export interface RouteQuery {
  page: number;
  pageSize: number;
  q: string;
  routeStatus: string;
  upstreamStatus: string;
  protocol: string;
  sort: string;
}

export async function listRoutes(upstreamId: number, query: RouteQuery, signal?: AbortSignal): Promise<PageResult<RouteRow>> {
  const raw = await api.get<unknown>(`${LLM}/upstreams/${seg(upstreamId)}/models`, {
    signal,
    query: { page: query.page, page_size: query.pageSize, q: query.q.trim(), route_status: query.routeStatus, upstream_status: query.upstreamStatus, protocol: query.protocol, sort: query.sort },
  });
  return readPage<RouteRow>(raw);
}

export interface BindingBody {
  upstreamModelName: string;
  platformModelName: string;
  kindsJSON: string;
  protocols: string[];
  routeIDs?: number[];
  status?: string;
  priority?: number;
  weight?: number;
}

export function upsertBinding(upstreamId: number, body: BindingBody): Promise<unknown> {
  return api.post(`${LLM}/upstreams/${seg(upstreamId)}/models`, body);
}

export async function setRouteStatus(upstreamId: number, routeId: number, status: "enable" | "disable"): Promise<void> {
  await api.patch(`${LLM}/upstreams/${seg(upstreamId)}/models/${seg(routeId)}/${status}`);
}

export async function deleteRoute(upstreamId: number, routeId: number): Promise<void> {
  await api.delete(`${LLM}/upstreams/${seg(upstreamId)}/models/${seg(routeId)}`);
}

export function batchDeleteRoutes(upstreamId: number, ids: number[]): Promise<BatchResult> {
  return api.post<BatchResult>(`${LLM}/upstreams/${seg(upstreamId)}/models/batch-delete`, { ids });
}

export async function routeCircuit(upstreamId: number, routeId: number, action: "open" | "reset"): Promise<void> {
  await api.post(`${LLM}/upstreams/${seg(upstreamId)}/models/${seg(routeId)}/circuit/${action}`);
}

// --- probes --------------------------------------------------------------------------

export interface ProbeDebug {
  request?: { method?: string; path?: string; headers?: Record<string, string>; body?: string };
  response?: { headers?: Record<string, string>; body?: string };
  [key: string]: unknown;
}

export interface ProbeResult {
  routeID: number;
  bindingCode: string;
  platformModelID: number;
  platformModelName: string;
  upstreamID: number;
  upstreamName: string;
  upstreamModelID: number;
  upstreamModelName: string;
  protocol: string;
  endpoint: string;
  status: "success" | "failed" | "unsupported" | (string & {});
  success: boolean;
  latencyMS: number;
  errorCode?: string;
  errorMessage?: string;
  upstreamStatusCode?: number;
  debug?: ProbeDebug;
}

export interface ProbeAll {
  totalCount: number;
  successCount: number;
  failedCount: number;
  unsupportedCount: number;
  results: ProbeResult[];
}

export function probeRoute(upstreamId: number, routeId: number): Promise<ProbeResult> {
  return api.post<ProbeResult>(`${LLM}/upstreams/${seg(upstreamId)}/models/${seg(routeId)}/test`, {});
}

export function probeModel(modelId: number): Promise<ProbeResult> {
  return api.post<ProbeResult>(`${LLM}/models/${seg(modelId)}/test`, {});
}

export function probeModelAll(modelId: number): Promise<ProbeAll> {
  return api.post<ProbeAll>(`${LLM}/models/${seg(modelId)}/test-all`, {});
}

// --- remote models ----------------------------------------------------------------

export interface RemoteItem {
  upstreamModelName: string;
  suggestedPlatformModelName: string;
  suggestedKindsJSON: string;
  suggestedProtocol: string;
  suggestedProtocols: string[];
  bindingCode: string;
  boundPlatformModels: string[];
  upstreamModelStatus: string;
  alreadySynced: boolean;
  alreadyBound: boolean;
}

export interface SyncPlan {
  addedModels: string[];
  updatedModels: string[];
  reactivatedModels: string[];
  inactivatedModels: string[];
  unchangedModels: string[];
  protectedModels: string[];
  unresolvedProtocolModels: string[];
}

export interface RemotePreview {
  total: number;
  items: RemoteItem[];
  snapshotID: string;
  syncPlan: SyncPlan;
}

export function previewRemote(upstreamId: number, signal?: AbortSignal): Promise<RemotePreview> {
  return api.get<RemotePreview>(`${LLM}/upstreams/${seg(upstreamId)}/models/remote`, { signal });
}

export interface SyncResult {
  snapshotID: string;
  totalUpstream: number;
  createdUpstreamModels: number;
  updatedUpstreamModels: number;
  unchangedUpstreamModels: number;
  protectedUpstreamModels: number;
  existingUpstreamModels: number;
  skippedUpstreamModels: number;
  inactivatedModels: number;
  reactivatedModels: number;
  unresolvedProtocolModels: string[];
  syncedModels: Array<{ upstreamModelName: string; bindingCode: string; suggestedProtocol: string; kindsJSON: string; status: string }>;
}

export function syncRemote(upstreamId: number, args: { allowEmpty: boolean; expectedSnapshot: string }, signal?: AbortSignal): Promise<SyncResult> {
  return api.post<SyncResult>(`${LLM}/upstreams/${seg(upstreamId)}/models/sync`, undefined, {
    signal,
    query: { allow_empty: args.allowEmpty ? true : undefined, expected_snapshot: args.expectedSnapshot },
  });
}

export interface ImportItem {
  upstreamModelName: string;
  platformModelName: string;
  protocols?: string[];
  kindsJSON?: string;
}

export interface ImportResult {
  total: number;
  importedCount: number;
  failedCount: number;
  createdPlatform: number;
  createdRoutes: number;
  existingRoutes: number;
  results: Array<{ upstreamModelName: string; platformModelName: string; protocols: string[]; status?: string; error?: string }>;
}

export function importRemote(upstreamId: number, body: { items: ImportItem[]; permissionGroupIDs?: number[] }, signal?: AbortSignal): Promise<ImportResult> {
  return api.post<ImportResult>(`${LLM}/upstreams/${seg(upstreamId)}/models/import`, body, { signal });
}

// --- models -----------------------------------------------------------------------
