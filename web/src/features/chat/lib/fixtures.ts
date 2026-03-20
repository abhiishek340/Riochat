import type { ChatMessage } from "../types";

/** Test helper: a minimal message. */
export function msg(partial: Partial<ChatMessage> & { id: string }): ChatMessage {
  return {
    key: `id:${partial.id}`,
    numericId: null,
    persisted: true,
    role: "user",
    parentId: null,
    sourceId: null,
    branchReason: "default",
    runId: "",
    content: "",
    contentType: "text",
    status: "success",
    attachments: [],
    usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, reasoning: 0 },
    reasoningEffort: null,
    latencyMs: 0,
    model: "",
    errorCode: "",
    errorMessage: "",
    errorRequestId: "",
    feedback: "",
    editedAt: null,
    createdAt: "2026-01-01T00:00:00Z",
    updatedAt: "2026-01-01T00:00:00Z",
    billing: null,
    sources: [],
    trace: null,
    moderation: null,
    uiState: {},
    alert: null,
    ...partial,
  };
}
