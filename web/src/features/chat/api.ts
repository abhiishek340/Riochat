/**
 * HTTP wrappers for everything the chat area calls. Paths are relative to
 * `/api/v1`; the shared client unwraps the envelope and handles tokens.
 */
import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import type { Paged } from "@/lib/api/types";
import type {
  ConversationDTO,
  FileDTO,
  FilePolicyDTO,
  KnowledgeBaseDTO,
  McpToolDTO,
  MessageDTO,
  ModelDTO,
  ModelOptionPolicyDTO,
  ProcessTraceDTO,
  ProjectDTO,
  PromptPresetDTO,
  RunDTO,
  SkillSummaryDTO,
  UiComponentDTO,
} from "./types";

// --- conversations -----------------------------------------------------------

export interface ConversationQuery {
  page?: number;
  pageSize?: number;
  status?: string;
  starred?: boolean;
  project?: string;
  q?: string;
}

export function listConversations(query: ConversationQuery = {}, signal?: AbortSignal): Promise<Paged<ConversationDTO>> {
  return api.get<Paged<ConversationDTO>>("/conversations", {
    signal,
    query: {
      page: query.page ?? 1,
      page_size: query.pageSize ?? 50,
      status: query.status,
      starred: query.starred === undefined ? undefined : String(query.starred),
      project: query.project,
      q: query.q,
    },
  });
}

export function getConversation(id: string, signal?: AbortSignal): Promise<ConversationDTO> {
  return api.get<ConversationDTO>(`/conversations/${seg(id)}`, { signal });
}

export function createConversation(body: { model?: string; projectID?: string; title?: string }): Promise<ConversationDTO> {
  return api.post<ConversationDTO>("/conversations", body);
}

export function renameConversation(id: string, title: string): Promise<ConversationDTO> {
  return api.patch<ConversationDTO>(`/conversations/${seg(id)}/title`, { title });
}

export function regenerateTitle(id: string): Promise<ConversationDTO> {
  return api.post<ConversationDTO>(`/conversations/${seg(id)}/title/regenerate`);
}

export function updateLabels(id: string, labels: string[]): Promise<ConversationDTO> {
  return api.patch<ConversationDTO>(`/conversations/${seg(id)}/labels`, { labels });
}

export function setStar(id: string, starred: boolean): Promise<ConversationDTO> {
  return api.patch<ConversationDTO>(`/conversations/${seg(id)}/star`, { starred });
}

export function setConversationProject(id: string, projectID: string): Promise<ConversationDTO> {
  return api.patch<ConversationDTO>(`/conversations/${seg(id)}/project`, { projectID });
}

export function deleteConversation(id: string, deleteFiles: boolean): Promise<{ deleted: boolean; deletedFileCount?: number }> {
  return api.delete(`/conversations/${seg(id)}`, { query: { delete_files: deleteFiles ? "true" : undefined } });
}

export function exportConversation(id: string): Promise<unknown> {
  return api.get(`/conversations/${seg(id)}/export`);
}

export function forkConversation(id: string, messageId: string): Promise<ConversationDTO> {
  return api.post<ConversationDTO>(`/conversations/${seg(id)}/messages/${seg(messageId)}/fork`);
}

export function defaultModelCandidate(signal?: AbortSignal): Promise<{ platformModelName: string }> {
  return api.get("/conversations/default-model-candidate", { signal });
}

// --- messages ------------------------------------------------------------------

export interface MessageQuery {
  page?: number;
  pageSize?: number;
  tail?: boolean;
  beforeId?: number;
  trace?: "summary";
}

export function listMessages(id: string, query: MessageQuery = {}, signal?: AbortSignal): Promise<Paged<MessageDTO>> {
  return api.get<Paged<MessageDTO>>(`/conversations/${seg(id)}/messages`, {
    signal,
    query: {
      page: query.page ?? 1,
      page_size: query.pageSize ?? 30,
      tail: query.tail ? "true" : undefined,
      before_id: query.beforeId,
      trace: query.trace,
    },
  });
}

export function editMessage(messageId: string, content: string): Promise<MessageDTO> {
  return api.patch<MessageDTO>(`/messages/${seg(messageId)}`, { content });
}

export function deleteMessage(conversationId: string, messageId: string): Promise<unknown> {
  return api.delete(`/conversations/${seg(conversationId)}/messages/${seg(messageId)}`);
}

export function setFeedback(messageId: string, feedback: "up" | "down" | ""): Promise<{ myFeedback: string }> {
  return api.put<{ myFeedback: string }>(`/messages/${seg(messageId)}/feedback`, feedback ? { feedback } : {});
}

export async function fetchMessageTrace(messageId: string, signal?: AbortSignal): Promise<ProcessTraceDTO | null> {
  const res = await api.get<{ processTrace: ProcessTraceDTO | null }>(`/messages/${seg(messageId)}/trace`, { signal });
  return res.processTrace ?? null;
}

export interface ToolCallDetail {
  errorJSON: string;
  errorOmitted: boolean;
  errorSizeBytes: number;
  outputJSON: string;
  outputOmitted: boolean;
  outputSizeBytes: number;
  runID: string;
  status: string;
  toolCallID: string;
  toolName: string;
}

export function fetchToolCall(runId: string, toolCallId: string, signal?: AbortSignal): Promise<ToolCallDetail> {
  return api.get<ToolCallDetail>(`/conversation-runs/${seg(runId)}/tool-calls/${seg(toolCallId)}`, { signal });
}

export function putUiState(
  conversationId: string,
  messageId: string,
  blocks: Record<string, Record<string, unknown> | null>,
  options: { keepalive?: boolean } = {},
): Promise<{ uiState: Record<string, unknown> }> {
  return api.put(`/conversations/${seg(conversationId)}/messages/${seg(messageId)}/ui-state`, { blocks }, { keepalive: options.keepalive });
}

// --- runs ------------------------------------------------------------------------

export function listRuns(conversationId: string, signal?: AbortSignal): Promise<Paged<RunDTO>> {
  return api.get<Paged<RunDTO>>(`/conversations/${seg(conversationId)}/runs`, { signal, query: { page: 1, page_size: 1 } });
}

export function cancelRun(runId: string): Promise<{ canceled: boolean }> {
  return api.post<{ canceled: boolean }>(`/conversation-runs/${seg(runId)}/cancel`);
}
