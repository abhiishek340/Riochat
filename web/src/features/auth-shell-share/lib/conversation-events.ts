/**
 * Conversation changes announced on `window`, so lists that do not share a
 * store (the sidebar, the all-conversations page, search) stay in step.
 * Anyone who changes a conversation may dispatch; anyone showing a list may
 * listen. Payloads carry only what changed.
 */
import type { ConversationRow } from "../recent/model";

export const CONVERSATION_CHANGED_EVENT = "riochat:conversation-changed";

export type ConversationChange =
  | { type: "remove"; ids: string[] }
  | { type: "patch"; id: string; patch: Partial<ConversationRow> }
  | { type: "upsert"; row: ConversationRow };

export function announceConversationChange(change: ConversationChange): void {
  if (typeof window !== "undefined") window.dispatchEvent(new CustomEvent(CONVERSATION_CHANGED_EVENT, { detail: change }));
}

export function subscribeConversationChanges(listener: (change: ConversationChange) => void): () => void {
  const handler = (event: Event) => {
    const detail = (event as CustomEvent<ConversationChange | undefined>).detail;
    if (detail && typeof detail === "object" && "type" in detail) listener(detail);
  };
  window.addEventListener(CONVERSATION_CHANGED_EVENT, handler);
  return () => window.removeEventListener(CONVERSATION_CHANGED_EVENT, handler);
}
