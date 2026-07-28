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

export interface ModelQuery {
  page: number;
  pageSize: number;
  q: string;
  status: string;
  vendor: string;
  protocol: string;
  upstream: string;
  sort: string;
  onlyAvailable?: boolean;
}

export async function listModels(query: ModelQuery, signal?: AbortSignal): Promise<PageResult<PlatformModel>> {
  const raw = await api.get<unknown>(`${LLM}/models`, {
    signal,
    query: { page: query.page, page_size: query.pageSize, q: query.q.trim(), status: query.status, vendor: query.vendor, protocol: query.protocol, upstream: query.upstream, sort: query.sort, only_available: query.onlyAvailable ? true : undefined },
  });
  return readPage<PlatformModel>(raw);
}

export async function createModel(body: Record<string, unknown>): Promise<PlatformModel> {
  const out = await api.post<{ model: PlatformModel }>(`${LLM}/models`, body);
  return out.model;
}

export async function updateModel(id: number, body: Record<string, unknown>): Promise<PlatformModel> {
  const out = await api.patch<{ model: PlatformModel }>(`${LLM}/models/${seg(id)}`, body);
  return out.model;
}

export async function deleteModel(id: number): Promise<void> {
  await api.delete(`${LLM}/models/${seg(id)}`);
}

export function batchDeleteModels(ids: number[]): Promise<BatchResult> {
  return api.post<BatchResult>(`${LLM}/models/batch-delete`, { ids });
}

export async function setModelProtocols(id: number, protocols: string[], kindsJSON: string): Promise<void> {
  await api.patch(`${LLM}/models/${seg(id)}/protocols`, { protocols, kindsJSON });
}

export async function assignDisplayGroup(modelIds: number[], displayGroupId: number): Promise<void> {
  await api.patch(`${LLM}/models/display-group`, { modelIDs: modelIds, displayGroupID: displayGroupId });
}

export async function saveModelOrder(modelIds: number[]): Promise<void> {
  await api.post(`${LLM}/models/order`, { modelIDs: modelIds });
}

export interface SourceView {
  id: number;
  upstreamID: number;
  upstreamName: string;
  upstreamStatus: string;
  baseURL: string;
  bindingCode: string;
  upstreamModelName: string;
  upstreamModelKindsJSON: string;
  upstreamModelVendor: string;
  upstreamModelIcon: string;
  suggestedProtocol: string;
  upstreamModelStatus: string;
  protocol: string;
  status: "active" | "inactive" | (string & {});
  priority: number;
  weight: number;
  source: string;
  cbFailureThreshold: number;
  cbDurationMin: number;
  cbWindowMin: number;
  headersJSON: string;
  circuitOpen: boolean;
  circuitUntil: string;
  circuitScope: "route" | "upstream" | (string & {});
  createdAt: string;
  updatedAt: string;
}

export async function listSources(modelId: number, page = 1, pageSize = 25, signal?: AbortSignal): Promise<PageResult<SourceView>> {
  return readPage<SourceView>(await api.get<unknown>(`${LLM}/models/${seg(modelId)}/sources`, { signal, query: { page, page_size: pageSize } }));
}

export async function bindSource(modelId: number, body: Record<string, unknown>): Promise<SourceView> {
  const out = await api.post<{ source?: SourceView } | SourceView>(`${LLM}/models/${seg(modelId)}/sources`, body);
  return ((out as { source?: SourceView }).source ?? out) as SourceView;
}

export async function updateSource(modelId: number, routeId: number, body: Record<string, unknown>): Promise<SourceView> {
  const out = await api.patch<{ source?: SourceView } | SourceView>(`${LLM}/models/${seg(modelId)}/sources/${seg(routeId)}`, body);
  return ((out as { source?: SourceView }).source ?? out) as SourceView;
}

// --- presentation -------------------------------------------------------------------

export interface Vendor {
  id: number;
  key: string;
  name: string;
  icon: string;
  builtIn: boolean;
  sortOrder: number;
}

export interface DisplayGroup {
  id: number;
  name: string;
  icon: string;
  sortOrder: number;
}

export async function listVendors(signal?: AbortSignal): Promise<Vendor[]> {
  return readPage<Vendor>(await api.get<unknown>(`${LLM}/model-vendors`, { signal, query: { page: 1, page_size: 1000 } })).results;
}

export async function createVendor(body: { key: string; name: string; icon: string }): Promise<Vendor> {
  const out = await api.post<{ vendor: Vendor }>(`${LLM}/model-vendors`, body);
  return out.vendor;
}

export async function updateVendor(key: string, body: { name?: string; icon?: string }): Promise<Vendor> {
  const out = await api.patch<{ vendor: Vendor }>(`${LLM}/model-vendors/${seg(key)}`, body);
  return out.vendor;
}

export async function deleteVendor(key: string): Promise<void> {
  await api.delete(`${LLM}/model-vendors/${seg(key)}`);
}

export async function listDisplayGroups(signal?: AbortSignal): Promise<DisplayGroup[]> {
  return readPage<DisplayGroup>(await api.get<unknown>(`${LLM}/model-display-groups`, { signal, query: { page: 1, page_size: 1000 } })).results;
}

export async function createDisplayGroup(body: { name: string; icon: string; modelIDs?: number[] }): Promise<DisplayGroup> {
  const out = await api.post<{ group: DisplayGroup }>(`${LLM}/model-display-groups`, body);
  return out.group;
}

export async function updateDisplayGroup(id: number, body: { name?: string; icon?: string; modelIDs?: number[] }): Promise<DisplayGroup> {
  const out = await api.patch<{ group: DisplayGroup }>(`${LLM}/model-display-groups/${seg(id)}`, body);
  return out.group;
}

export async function deleteDisplayGroup(id: number): Promise<void> {
  await api.delete(`${LLM}/model-display-groups/${seg(id)}`);
}

export interface IconAsset {
  ref: string;
  publicID: string;
  contentType: string;
  sizeBytes: number;
  width: number;
  height: number;
  reused?: boolean;
}

export async function listIconAssets(signal?: AbortSignal): Promise<IconAsset[]> {
  return readPage<IconAsset>(await api.get<unknown>(`${LLM}/icon-assets`, { signal, query: { page: 1, page_size: 100 } })).results;
}

export function uploadIconAsset(file: File): Promise<IconAsset> {
  const body = new FormData();
  body.append("file", file, file.name);
  return api.post<IconAsset>(`${LLM}/icon-assets`, body);
}

export async function deleteIconAsset(publicId: string): Promise<void> {
  await api.delete(`${LLM}/icon-assets/${seg(publicId)}`);
}

// --- catalog and global settings ----------------------------------------------------------

export interface CatalogStatus {
  origin: string | null;
  fetchedAt: string | null;
  modelCount: number;
  lastError: string | null;
  refreshing: boolean;
}

export function catalogStatus(signal?: AbortSignal): Promise<CatalogStatus> {
  return api.get<CatalogStatus>(`${LLM}/model-catalog`, { signal });
}

export function refreshCatalog(): Promise<CatalogStatus> {
  return api.post<CatalogStatus>(`${LLM}/model-catalog/refresh`);
}

export interface ResolvedReasoning {
  format: string;
  levels: string[];
  default: string;
  source: string;
}

export interface ReasoningTemplate {
  format: string;
  levels: string[];
  budgets?: Record<string, number>;
}

export interface CatalogResolve {
  matched: boolean;
  provider: string | null;
  modelId: string | null;
  inputModalities: string[];
  outputModalities: string[];
  contextWindow: number | null;
  reasoning: ResolvedReasoning | null;
  reasoningTemplate: ReasoningTemplate | null;
}

export function resolveCatalog(body: { name: string; vendor: string; protocols: string[]; capabilitiesJSON: string }, signal?: AbortSignal): Promise<CatalogResolve> {
  return api.post<CatalogResolve>(`${LLM}/model-catalog/resolve`, body, { signal });
}

export interface LlmSetting {
  id: number;
  key: string;
  value: string;
  description: string;
}

export async function listLlmSettings(signal?: AbortSignal): Promise<LlmSetting[]> {
  const out = await api.get<LlmSetting[] | null>(`${LLM}/settings`, { signal });
  return out ?? [];
}

export function patchLlmSetting(key: string, value: string): Promise<LlmSetting> {
  return api.patch<LlmSetting>(`${LLM}/settings/${seg(key)}`, { value });
}

// --- permission groups ------------------------------------------------------------------

export interface GroupRule {
  type: "all" | "vendor" | "protocol" | "upstream" | (string & {});
  value: string;
}

export interface GroupInputBody {
  name: string;
  description?: string;
  rateMultiplierPercent?: number;
}

export async function createGroup(body: GroupInputBody) {
  const out = await api.post<{ group: PermissionGroup }>("/admin/permission-groups", body);
  return out.group;
}

export async function patchGroup(id: number, body: GroupInputBody) {
  const out = await api.patch<{ group: PermissionGroup }>(`/admin/permission-groups/${seg(id)}`, body);
  return out.group;
}

export interface GroupDeletion {
  manualModelCount: number;
  ruleCount: number;
  manualUserCount: number;
  planCount: number;
}

export async function deleteGroup(id: number): Promise<GroupDeletion> {
  const out = await api.delete<{ summary?: GroupDeletion }>(`/admin/permission-groups/${seg(id)}`);
  return out?.summary ?? { manualModelCount: 0, ruleCount: 0, manualUserCount: 0, planCount: 0 };
}

export function getGroupModels(id: number, signal?: AbortSignal): Promise<{ modelIDs: number[]; rules: GroupRule[] }> {
  return api.get(`/admin/permission-groups/${seg(id)}/models`, { signal });
}

export function putGroupModels(id: number, modelIds: number[], rules: GroupRule[]): Promise<{ modelIDs: number[]; rules: GroupRule[] }> {
  return api.put(`/admin/permission-groups/${seg(id)}/models`, { modelIDs: modelIds, rules });
}

export function getGroupUsers(id: number, signal?: AbortSignal): Promise<{ userIDs: number[] }> {
  return api.get(`/admin/permission-groups/${seg(id)}/users`, { signal });
}

export function putGroupUsers(id: number, userIds: number[]): Promise<{ userIDs: number[] }> {
  return api.put(`/admin/permission-groups/${seg(id)}/users`, { userIDs: userIds });
}

export interface ModelGroupReach {
  manualGroupIDs: number[];
  matchedGroupIDs: number[];
  effectiveGroupIDs: number[];
  unassigned: boolean;
}

export function getModelGroups(modelId: number, signal?: AbortSignal): Promise<ModelGroupReach> {
  return api.get(`/admin/models/${seg(modelId)}/permission-groups`, { signal });
}

export function putModelGroups(modelId: number, groupIds: number[]): Promise<ModelGroupReach> {
  return api.put(`/admin/models/${seg(modelId)}/permission-groups`, { groupIDs: groupIds });
}
