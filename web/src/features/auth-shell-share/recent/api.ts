import { api } from "@/lib/api/client";
import type { Paged } from "@/lib/api/types";
import { isRecord, str } from "@/lib/utils/guards";
import { listQuery, readConversation, type ConversationRow, type Filters } from "./model";

export interface ConversationPage {
  rows: ConversationRow[];
  total: number;
  received: number;
}

export async function fetchConversationPage(filters: Filters, page: number, signal?: AbortSignal): Promise<ConversationPage> {
  const data = await api.get<Paged<unknown>>("/conversations", { signal, query: listQuery(filters, page) });
  const results = Array.isArray(data?.results) ? data.results : [];
  const rows = results.map(readConversation).filter((r): r is ConversationRow => r !== null);
  return { rows, total: typeof data?.total === "number" ? data.total : rows.length, received: results.length };
}

export interface ProjectRef {
  publicID: string;
  name: string;
}

export async function fetchProjects(signal?: AbortSignal): Promise<ProjectRef[]> {
  const data = await api.get<unknown>("/conversation-projects", { signal });
  const list = Array.isArray(data) ? data : isRecord(data) && Array.isArray(data.results) ? data.results : [];
  return list
    .filter(isRecord)
    .map((p) => ({ publicID: str(p.publicID), name: str(p.name) }))
    .filter((p) => p.publicID !== "");
}

export async function fetchDeleteFilesDefault(): Promise<boolean> {
  try {
    const data = await api.get<unknown>("/user/settings");
    const settings = isRecord(data) && isRecord(data.settings) ? data.settings : {};
    return settings["chat.delete_conversation_files_by_default"] === "true";
  } catch {
    return false;
  }
}

const enc = encodeURIComponent;

export function setStarred(id: string, starred: boolean): Promise<unknown> {
  return api.patch(`/conversations/${enc(id)}/star`, { starred });
}

export function setArchived(id: string, archived: boolean): Promise<unknown> {
  return api.patch(`/conversations/${enc(id)}/archive`, { archived });
}

export function renameConversation(id: string, title: string): Promise<unknown> {
  return api.patch(`/conversations/${enc(id)}/title`, { title });
}

export async function regenerateTitle(id: string): Promise<string> {
  const data = await api.post<unknown>(`/conversations/${enc(id)}/title/regenerate`);
  return isRecord(data) ? str(data.title) : "";
}

export function setLabels(id: string, labels: string[]): Promise<unknown> {
  return api.patch(`/conversations/${enc(id)}/labels`, { labels });
}

export function moveToProject(id: string, projectId: string): Promise<unknown> {
  return api.patch(`/conversations/${enc(id)}/project`, { projectID: projectId });
}

/** Resolves to how many conversations the server actually moved. */
export async function moveManyToProject(ids: string[], projectId: string): Promise<number> {
  const data = await api.post<unknown>("/conversations/project", { conversationPublicIDs: ids, projectID: projectId });
  return isRecord(data) && typeof data.updated === "number" ? data.updated : 0;
}

export function deleteConversation(id: string, deleteFiles: boolean): Promise<unknown> {
  return api.delete(`/conversations/${enc(id)}`, { query: { delete_files: deleteFiles ? "true" : undefined } });
}

export function revokeShares(ids: string[]): Promise<unknown> {
  return api.post("/conversations/shares/revoke", { conversationPublicIDs: ids });
}

export async function exportConversationJson(id: string): Promise<Blob> {
  const response = await api.raw(`/conversations/${enc(id)}/export`);
  return response.blob();
}

export async function exportAllJsonl(): Promise<string> {
  const response = await api.raw("/conversations/export", { headers: { Accept: "application/x-ndjson" } });
  return response.text();
}

export interface ShareInfo {
  shareID: string;
  status: string;
  createdAt: string;
  lastAccessedAt: string | null;
}

function readShare(data: unknown): ShareInfo | null {
  if (!isRecord(data)) return null;
  const shareID = str(data.shareID);
  if (!shareID) return null;
  return {
    shareID,
    status: str(data.status),
    createdAt: str(data.createdAt),
    lastAccessedAt: typeof data.lastAccessedAt === "string" ? data.lastAccessedAt : null,
  };
}

export async function fetchShare(id: string): Promise<ShareInfo | null> {
  try {
    return readShare(await api.get<unknown>(`/conversations/${enc(id)}/share`));
  } catch (error) {
    if (typeof error === "object" && error !== null && (error as { status?: number }).status === 404) return null;
    throw error;
  }
}

export async function createShare(id: string, regenerate: boolean): Promise<ShareInfo> {
  const data = await api.post<unknown>(`/conversations/${enc(id)}/share${regenerate ? "/regenerate" : ""}`);
  const share = readShare(data);
  if (!share) throw new Error("errors.generic");
  return share;
}

export function revokeShare(id: string): Promise<unknown> {
  return api.delete(`/conversations/${enc(id)}/share`);
}
