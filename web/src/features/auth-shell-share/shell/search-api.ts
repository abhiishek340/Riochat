import { api } from "@/lib/api/client";
import { isRecord, str } from "@/lib/utils/guards";
import { SEARCH_PAGE_SIZE, type SearchItem } from "../lib/search-model";

export interface SearchPage {
  results: SearchItem[];
  hasMore: boolean;
}

function readItem(raw: unknown): SearchItem | null {
  if (!isRecord(raw)) return null;
  const publicID = str(raw.publicID);
  if (!publicID) return null;
  return {
    publicID,
    title: str(raw.title),
    projectName: str(raw.projectName),
    projectID: str(raw.projectID),
    status: str(raw.status),
    updatedAt: str(raw.updatedAt),
    isStarred: raw.isStarred === true,
    messageCount: typeof raw.messageCount === "number" ? raw.messageCount : 0,
  };
}

export async function searchConversations(input: { page: number; query: string }, signal?: AbortSignal): Promise<SearchPage> {
  const data = await api.get<unknown>("/conversations/search", {
    signal,
    query: { page: input.page, page_size: SEARCH_PAGE_SIZE, q: input.query || undefined },
  });
  const d = isRecord(data) ? data : {};
  const results = (Array.isArray(d.results) ? d.results : []).map(readItem).filter((i): i is SearchItem => i !== null);
  return { results, hasMore: d.hasMore === true };
}

export interface PreviewMessage {
  publicID: string;
  role: "user" | "assistant";
  content: string;
  errorMessage: string;
}

export async function fetchPreview(conversationId: string, signal?: AbortSignal): Promise<PreviewMessage[]> {
  const data = await api.get<unknown>(`/conversations/${encodeURIComponent(conversationId)}/messages/preview`, { signal });
  const out: PreviewMessage[] = [];
  for (const raw of Array.isArray(data) ? data : []) {
    if (!isRecord(raw)) continue;
    const role = raw.role === "user" ? "user" : raw.role === "assistant" ? "assistant" : null;
    if (!role) continue;
    const content = str(raw.content);
    const errorMessage = str(raw.errorMessage);
    // Messages with nothing to show are left out.
    if (!content.trim() && !errorMessage.trim()) continue;
    out.push({ publicID: str(raw.publicID), role, content, errorMessage });
  }
  return out;
}
