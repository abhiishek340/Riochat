/**
 * The user's conversations and projects as the sidebar and the chat page see
 * them. One store for the whole app: the sidebar lists from it, the chat engine
 * patches it after sends (optimistic title, counts, labels).
 */
import { createConversation, deleteConversation, getConversation, listConversations, listProjects } from "../api";
import type { ConversationDTO, ProjectDTO } from "../types";
import { createStore } from "./store";

export interface ConversationListState {
  conversations: ConversationDTO[];
  projects: ProjectDTO[];
  total: number;
  page: number;
  loaded: boolean;
  loading: boolean;
  error: unknown;
}

const PAGE_SIZE = 50;

export const conversationStore = createStore<ConversationListState>({
  conversations: [],
  projects: [],
  total: 0,
  page: 0,
  loaded: false,
  loading: false,
  error: null,
});

function byRecency(a: ConversationDTO, b: ConversationDTO): number {
  return Date.parse(b.updatedAt) - Date.parse(a.updatedAt);
}

export function mergeConversations(current: readonly ConversationDTO[], incoming: readonly ConversationDTO[]): ConversationDTO[] {
  const map = new Map(current.map((c) => [c.publicID, c] as const));
  for (const c of incoming) map.set(c.publicID, c);
  return [...map.values()].sort(byRecency);
}

let inflight: Promise<void> | null = null;

/** Loads the first page again (also used after a window regains focus). */
export function refreshConversations(): Promise<void> {
  if (inflight) return inflight;
  conversationStore.set((s) => ({ ...s, loading: true }));
  inflight = (async () => {
    try {
      const [page, projects] = await Promise.all([
        listConversations({ page: 1, pageSize: PAGE_SIZE, status: "active" }),
        listProjects().catch(() => null),
      ]);
      conversationStore.set((s) => {
        // Keep entries from further pages and entries added locally.
        const local = s.conversations.filter((c) => !page.results.some((p) => p.publicID === c.publicID));
        const kept = s.page > 1 ? local : local.filter((c) => Date.now() - Date.parse(c.updatedAt) < 60_000);
        return {
          ...s,
          conversations: mergeConversations(kept, page.results),
          projects: projects ?? s.projects,
          total: page.total,
          page: Math.max(1, s.page),
          loaded: true,
          loading: false,
          error: null,
        };
      });
    } catch (error) {
      conversationStore.set((s) => ({ ...s, loading: false, loaded: true, error }));
    } finally {
      inflight = null;
    }
  })();
  return inflight;
}

export async function loadMoreConversations(): Promise<void> {
  const s = conversationStore.get();
  if (s.loading || s.conversations.length >= s.total) return;
  const next = s.page + 1;
  conversationStore.set((x) => ({ ...x, loading: true }));
  try {
    const page = await listConversations({ page: next, pageSize: PAGE_SIZE, status: "active" });
    conversationStore.set((x) => ({ ...x, conversations: mergeConversations(x.conversations, page.results), total: page.total, page: next, loading: false }));
  } catch (error) {
    conversationStore.set((x) => ({ ...x, loading: false, error }));
  }
}

export function upsertConversation(c: ConversationDTO): void {
  conversationStore.set((s) => ({ ...s, conversations: mergeConversations(s.conversations, [c]) }));
}

export function patchConversation(id: string, patch: Partial<ConversationDTO>): void {
  conversationStore.set((s) => {
    if (!s.conversations.some((c) => c.publicID === id)) return s;
    return { ...s, conversations: s.conversations.map((c) => (c.publicID === id ? { ...c, ...patch } : c)) };
  });
}

export function removeConversation(id: string): void {
  conversationStore.set((s) => ({ ...s, conversations: s.conversations.filter((c) => c.publicID !== id), total: Math.max(0, s.total - 1) }));
}

export function findConversation(id: string | null | undefined): ConversationDTO | null {
  if (!id) return null;
  return conversationStore.get().conversations.find((c) => c.publicID === id) ?? null;
}

/** The list entry, else a one-off fetch (conversations opened by link). */
export async function ensureConversation(id: string): Promise<ConversationDTO | null> {
  const hit = findConversation(id);
  if (hit) return hit;
  try {
    const fresh = await getConversation(id);
    upsertConversation(fresh);
    return fresh;
  } catch {
    return null;
  }
}

export async function createInList(input: { model?: string; projectID?: string }): Promise<ConversationDTO> {
  const created = await createConversation({ model: input.model, projectID: input.projectID || undefined });
  upsertConversation(created);
  conversationStore.set((s) => ({ ...s, total: s.total + 1 }));
  return created;
}

export async function deleteInList(id: string, deleteFiles: boolean): Promise<void> {
  await deleteConversation(id, deleteFiles);
  removeConversation(id);
}

// --- projects --------------------------------------------------------------------

export function setProjects(projects: ProjectDTO[]): void {
  conversationStore.set((s) => ({ ...s, projects }));
}

export function upsertProject(project: ProjectDTO): void {
  conversationStore.set((s) => {
    const has = s.projects.some((p) => p.publicID === project.publicID);
    const projects = has ? s.projects.map((p) => (p.publicID === project.publicID ? project : p)) : [...s.projects, project];
    return { ...s, projects };
  });
}

export function removeProject(id: string): void {
  conversationStore.set((s) => ({ ...s, projects: s.projects.filter((p) => p.publicID !== id) }));
}

export function resetConversationStore(): void {
  conversationStore.set({ conversations: [], projects: [], total: 0, page: 0, loaded: false, loading: false, error: null });
}

export function parseLabels(json: string | undefined): string[] {
  if (!json) return [];
  try {
    const parsed: unknown = JSON.parse(json);
    return Array.isArray(parsed) ? parsed.filter((x): x is string => typeof x === "string") : [];
  } catch {
    return [];
  }
}
