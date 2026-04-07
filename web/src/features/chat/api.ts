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

export interface RunStatus {
  runID: string;
  status: string;
}

export function runStatuses(runIds: string[], signal?: AbortSignal): Promise<RunStatus[]> {
  return api.post<RunStatus[]>("/conversation-runs/statuses", { runIDs: runIds }, { signal });
}

// --- projects --------------------------------------------------------------------

export function listProjects(signal?: AbortSignal): Promise<ProjectDTO[]> {
  return api.get<ProjectDTO[]>("/conversation-projects", { signal, query: { status: "active" } });
}

export type ProjectInput = Partial<
  Pick<
    ProjectDTO,
    "name" | "description" | "systemPrompt" | "defaultModel" | "mcpDefaultMode" | "defaultMCPToolIDs" | "defaultSkillIDs" | "defaultKnowledgeBaseIDs" | "color" | "icon"
  >
>;

export function createProject(input: ProjectInput & { name: string }): Promise<ProjectDTO> {
  return api.post<ProjectDTO>("/conversation-projects", input);
}

export function updateProject(id: string, input: ProjectInput & { status?: string }): Promise<ProjectDTO> {
  return api.patch<ProjectDTO>(`/conversation-projects/${seg(id)}`, input);
}

export function deleteProject(id: string, options: { deleteConversations?: boolean; deleteFiles?: boolean } = {}): Promise<unknown> {
  return api.delete(`/conversation-projects/${seg(id)}`, {
    query: {
      delete_conversations: options.deleteConversations ? "true" : undefined,
      delete_files: options.deleteFiles ? "true" : undefined,
    },
  });
}

// --- sharing ---------------------------------------------------------------------

export interface ShareDTO {
  shareID: string;
  status: string;
  titleSnapshot: string;
  messageCount: number;
  createdAt: string;
  updatedAt: string;
}

export function getShare(conversationId: string): Promise<ShareDTO> {
  return api.get<ShareDTO>(`/conversations/${seg(conversationId)}/share`);
}

export function createShare(conversationId: string, defaultMessagePublicIDs: string[], regenerate = false): Promise<ShareDTO> {
  return api.post<ShareDTO>(`/conversations/${seg(conversationId)}/share${regenerate ? "/regenerate" : ""}`, {
    defaultMessagePublicIDs,
  });
}

export function revokeShare(conversationId: string): Promise<ShareDTO> {
  return api.delete<ShareDTO>(`/conversations/${seg(conversationId)}/share`);
}

// --- models and policies ---------------------------------------------------------

export function listModels(signal?: AbortSignal): Promise<ModelDTO[]> {
  return api.get<ModelDTO[]>("/models", { signal });
}

export function getModelOptionPolicy(signal?: AbortSignal): Promise<ModelOptionPolicyDTO> {
  return api.get<ModelOptionPolicyDTO>("/settings/model-option-policy", { signal });
}

export function getMcpPolicy(signal?: AbortSignal): Promise<{ maxSelectedToolsPerMessage: number }> {
  return api.get("/settings/mcp-policy", { signal });
}

export async function getBillingConfig(signal?: AbortSignal): Promise<{ mode: string; displayCurrency: string; usdToCNYRate: number }> {
  const res = await api.get<{ config: { mode?: string; displayCurrency?: string; usdToCNYRate?: number } }>("/billing/config", { signal });
  return {
    mode: res.config?.mode ?? "self",
    displayCurrency: res.config?.displayCurrency === "CNY" ? "CNY" : "USD",
    usdToCNYRate: typeof res.config?.usdToCNYRate === "number" && res.config.usdToCNYRate > 0 ? res.config.usdToCNYRate : 1,
  };
}

// --- composer resources ------------------------------------------------------------

export async function listMcpTools(signal?: AbortSignal): Promise<McpToolDTO[]> {
  const res = await api.get<{ results: McpToolDTO[] }>("/mcp/tools", { signal });
  return res.results ?? [];
}

export function listSkills(query: { q?: string; page?: number; pageSize?: number; ids?: number[] } = {}, signal?: AbortSignal): Promise<Paged<SkillSummaryDTO>> {
  return api.get<Paged<SkillSummaryDTO>>("/skills", {
    signal,
    query: { q: query.q, page: query.page ?? 1, page_size: query.pageSize ?? 50, id: query.ids },
  });
}

export function listPromptPresets(query: { q?: string; page?: number; pageSize?: number } = {}, signal?: AbortSignal): Promise<Paged<PromptPresetDTO>> {
  return api.get<Paged<PromptPresetDTO>>("/prompt-presets", {
    signal,
    query: { q: query.q, page: query.page ?? 1, page_size: query.pageSize ?? 50 },
  });
}

export function listKnowledgeBases(query: { q?: string; page?: number; pageSize?: number } = {}, signal?: AbortSignal): Promise<Paged<KnowledgeBaseDTO>> {
  return api.get<Paged<KnowledgeBaseDTO>>("/knowledge-bases", {
    signal,
    query: { q: query.q, page: query.page ?? 1, page_size: query.pageSize ?? 50 },
  });
}

export function listUiComponents(signal?: AbortSignal): Promise<Paged<UiComponentDTO & { rendererSource?: string; propsSchema?: string; propsSummary?: string }>> {
  return api.get("/ui-components", { signal, query: { page: 1, page_size: 100 } });
}

// --- files ---------------------------------------------------------------------------

export function getFilePolicy(signal?: AbortSignal): Promise<FilePolicyDTO> {
  return api.get<FilePolicyDTO>("/runtime/chat-file-policy", { signal });
}

export function listFiles(query: { q?: string; page?: number; pageSize?: number; sort?: string } = {}, signal?: AbortSignal): Promise<Paged<FileDTO>> {
  return api.get<Paged<FileDTO>>("/files", {
    signal,
    query: { q: query.q, page: query.page ?? 1, page_size: query.pageSize ?? 20, sort: query.sort },
  });
}

export function uploadFile(file: File, signal?: AbortSignal): Promise<{ file: FileDTO; reused: boolean }> {
  const form = new FormData();
  form.append("purpose", "conversation_attachment");
  form.append("file", file, file.name);
  return api.post<{ file: FileDTO; reused: boolean }>("/files", form, { signal });
}

export interface FileProcessingDTO {
  fileID: string;
  detectedMIME: string;
  fileCategory: string;
  processingStatus: string;
  processingReady: boolean;
  extractStatus: string;
  embedStatus: string;
  ragReady: boolean;
  ragReason: string;
  errorCode: string;
  errorMessage?: string;
}

export function fileProcessingStatuses(fileIds: string[], signal?: AbortSignal): Promise<FileProcessingDTO[]> {
  return api.post<FileProcessingDTO[]>("/files/processing/statuses", { fileIDs: fileIds }, { signal });
}

// --- user settings and memory ----------------------------------------------------------

export async function getUserSettings(signal?: AbortSignal): Promise<Record<string, string>> {
  const res = await api.get<{ settings: Record<string, string> }>("/user/settings", { signal });
  return res.settings ?? {};
}

export async function patchUserSettings(settings: Record<string, string>): Promise<Record<string, string>> {
  const res = await api.patch<{ settings: Record<string, string> }>("/user/settings", { settings });
  return res.settings ?? {};
}

export function savePreferenceMemory(key: string, value: string): Promise<unknown> {
  return api.put("/memories/profile", { memoryKey: key, value, scope: "preference" });
}
