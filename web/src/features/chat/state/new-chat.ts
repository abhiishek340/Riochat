"use client";

import { createStore, useStore } from "./store";

/**
 * "Start a new chat" requests. The revision only ever grows, so the page can
 * tell a fresh request from an old one even when the address did not change.
 */
export interface NewChatRequest {
  revision: number;
  projectId: string | null;
}

export const newChatStore = createStore<NewChatRequest>({ revision: 0, projectId: null });

export function requestNewChat(projectId?: string | null): void {
  newChatStore.set((s) => ({ revision: s.revision + 1, projectId: projectId ?? null }));
}

export function useNewChatRequest(): NewChatRequest {
  return useStore(newChatStore);
}
