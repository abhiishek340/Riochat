import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import type { CategoryCatalog, ModerationConfig, ProbeResult, StatItem, UpdateBody } from "./rules";

const BASE = "/admin/content-moderation";

export async function getConfig(signal?: AbortSignal): Promise<{ config: ModerationConfig; categories: CategoryCatalog }> {
  const out = await api.get<{ config: ModerationConfig; categories?: CategoryCatalog }>(`${BASE}/config`, { signal });
  return { config: out.config, categories: out.categories ?? { text: [], image: [] } };
}

export async function putConfig(body: UpdateBody): Promise<ModerationConfig> {
  const out = await api.put<{ config: ModerationConfig }>(`${BASE}/config`, body);
  return out.config;
}

export function probeService(): Promise<ProbeResult> {
  return api.post<ProbeResult>(`${BASE}/probe`);
}

export async function getStats(range: { from?: string; to?: string }, signal?: AbortSignal): Promise<StatItem[]> {
  const out = await api.get<{ items?: StatItem[] }>(`${BASE}/stats`, { signal, query: { from: range.from, to: range.to } });
  return out?.items ?? [];
}

export interface ModerationEvent {
  publicID: string;
  runID: string;
  conversationID: number;
  messagePublicID: string;
  userID: number;
  username?: string;
  userLabel?: string;
  direction: string;
  modality: string;
  result: string;
  categories: string[];
  model: string;
  policyVersion: number;
  latencyMS: number;
  contentSummary: string;
  errorCode: string;
  errorMessage: string;
  createdAt: string;
}

export interface EventQuery {
  page: number;
  pageSize: number;
  query: string;
  result: string;
  direction: string;
}

export async function listEvents(query: EventQuery, signal?: AbortSignal): Promise<{ items: ModerationEvent[]; total: number }> {
  const digits = /^\d+$/.test(query.query.trim());
  const out = await api.get<{ items?: ModerationEvent[]; total?: number }>(`${BASE}/events`, {
    signal,
    query: { page: query.page, page_size: query.pageSize, query: digits ? undefined : query.query.trim(), user_id: digits ? query.query.trim() : undefined, result: query.result, direction: query.direction },
  });
  return { items: out?.items ?? [], total: out?.total ?? 0 };
}

export interface EventImage {
  index: number;
  sha256: string;
  mimeType: string;
  sizeBytes: number;
  sourceFileID?: string;
}

export interface EventDetail {
  event: ModerationEvent;
  categoryScores: Record<string, number>;
  textAvailable: boolean;
  decryptedText?: string;
  imagesAvailable: boolean;
  images: EventImage[];
}

export function getEvent(id: string, signal?: AbortSignal): Promise<EventDetail> {
  return api.get<EventDetail>(`${BASE}/events/${seg(id)}`, { signal });
}

/** Fetches an isolated image with the bearer token; callers own (and must revoke) the object URL. */
export async function fetchEventImage(id: string, index: number, signal?: AbortSignal): Promise<string> {
  const response = await api.raw(`${BASE}/events/${seg(id)}/images/${seg(index)}`, { signal, headers: { Accept: "image/*" } });
  const blob = await response.blob();
  const typed = blob.type ? blob : new Blob([blob], { type: "image/png" });
  return URL.createObjectURL(typed);
}
