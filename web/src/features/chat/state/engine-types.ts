/** Shared types of the chat engine. */
import type { Paged } from "@/lib/api/types";
import type { ActivityLabel, ChatAttachment, ChatMessage, ConversationDTO, MessageDTO, RunDTO } from "../types";
import type { BranchReason } from "../types";
import type { Navigator, Selection } from "../lib/tree";
import type { Options } from "../lib/options";
import type { ControlChoices } from "../lib/controls";
import type { Selections, UiStateEntry } from "../lib/request";
import type { RunStatus } from "../api";
import type { RunStreamHandlers, StreamOutcome } from "../lib/stream-consume";
import type { AttachmentFacts, ModelFacts, TaskKind } from "../lib/task-decision";
import type { RunRegistry } from "../lib/run-registry";

export const DRAFT_KEY = "draft";
export const TEMP_KEY = "temp";

export type NoticeCode =
  | "concurrency"
  | "noModel"
  | "blocked"
  | "attachmentsTruncated"
  | "createFailed"
  | "sendFailed"
  | "queuedCanceled"
  | "cannotRetry"
  | "cannotContinue"
  | "retryNoUser"
  | "moderation"
  | "stopFailed";

export interface Notice {
  code: NoticeCode;
  count?: number;
  block?: string;
  error?: unknown;
  /** Display text for a failed send (status-prefixed summary). */
  summary?: string;
}

export interface CompletedEvent {
  conversationId: string;
  text: string;
  title: string;
}

export interface EngineEvents {
  notice: Notice;
  conversationCreated: { fromKey: string; conversation: ConversationDTO };
  completed: CompletedEvent;
  runStarted: { runId: string; conversationId: string };
  runFinished: { runId: string; conversationId: string };
}

export type Listener<T> = (payload: T) => void;

export interface ConversationPort {
  create(input: { model: string; projectID: string }): Promise<ConversationDTO>;
  find(id: string): ConversationDTO | null;
  patch(id: string, patch: Partial<ConversationDTO>): void;
  /** Re-fetch the record (used after failures and by metadata polling). */
  fetch(id: string): Promise<ConversationDTO>;
  rename(id: string, title: string): Promise<ConversationDTO>;
}

export interface MessagesPort {
  list(id: string, query: { page: number; pageSize: number; tail?: boolean; beforeId?: number; trace?: "summary" }, signal?: AbortSignal): Promise<Paged<MessageDTO>>;
  cancelRun(runId: string): Promise<{ canceled: boolean }>;
  runStatuses(ids: string[], signal?: AbortSignal): Promise<RunStatus[]>;
  listRuns(conversationId: string): Promise<Paged<RunDTO>>;
}

export interface StreamPort {
  post(path: string, body: unknown, signal: AbortSignal, handlers: RunStreamHandlers): Promise<StreamOutcome>;
  resume(runId: string, after: number, signal: AbortSignal, handlers: RunStreamHandlers): Promise<StreamOutcome>;
}

export interface EngineDeps {
  conversations: ConversationPort;
  messages: MessagesPort;
  streams: StreamPort;
  runs: RunRegistry;
  hasToken: () => boolean;
  now: () => number;
  revokeUrl: (url: string) => void;
  /** Called before a send leaves: block states to report with the next turn. */
  collectUiStates: (path: readonly ChatMessage[]) => UiStateEntry[];
  isDocumentVisible: () => boolean;
}

export interface TurnLive {
  usage: { input: number; output: number; cacheRead: number; cacheWrite: number; reasoning: number };
}

export interface Turn {
  runId: string;
  key: string;
  conversationId: string | null;
  task: TaskKind;
  assistantOnly: boolean;
  user: ChatMessage | null;
  assistant: ChatMessage;
  /** Server copies once the final result is known. */
  result: { user: ChatMessage | null; assistant: ChatMessage | null } | null;
  stage: "running" | "settled" | "failed" | "aborted";
  attachments: ChatAttachment[];
  optimisticTitle: string;
  controller: AbortController;
  cancelRequested: boolean;
  cancelTimer: ReturnType<typeof setTimeout> | null;
  startedAt: number;
  order: number;
  /** The stream produced a terminal result. */
  terminal: boolean;
  fromComposer: boolean;
  /** Keep the previews alive (they went back to the composer). */
  restored: boolean;
  model: string;
  branchReason: BranchReason;
  /** The server's hint about title and label generation. */
  hint?: string;
}

export interface AttachState {
  runId: string;
  controller: AbortController;
  cursor: number;
  /** Replayed answer so far; null until the first event. */
  text: string | null;
  usage: TurnLive["usage"];
  trace: ChatMessage["trace"];
  label: ActivityLabel | null;
  contentType: string | null;
  blocked: boolean;
  preview?: string;
}

export interface ConvState {
  key: string;
  server: ChatMessage[];
  total: number;
  pagesLoaded: number;
  loading: boolean;
  loadedOnce: boolean;
  loadError: unknown;
  loadingOlder: boolean;
  turns: Turn[];
  selection: Selection;
  attach: AttachState | null;
  version: number;
  loadSeq: number;
  signature: string;
}

export interface ChatView {
  key: string;
  messages: ChatMessage[];
  path: ChatMessage[];
  navigators: Map<string, Navigator>;
  selection: Selection;
  loading: boolean;
  loadError: unknown;
  loadedOnce: boolean;
  hasOlder: boolean;
  loadingOlder: boolean;
  /** A reply is running (or pending) on the visible branch. */
  busy: boolean;
  /** This browser is streaming the visible reply. */
  streamingHere: boolean;
  /** The conversation has something to show or a send is in progress. */
  hasContent: boolean;
  runIds: string[];
}

export interface SendParams {
  key: string;
  projectId?: string | null;
  text: string;
  attachments: ChatAttachment[];
  model: string;
  modelFacts: ModelFacts;
  options: Options;
  optionPolicyMode?: string;
  controls?: ControlChoices;
  selections: Selections;
  parentId: string | null;
  sourceId: string | null;
  branchReason: BranchReason;
  assistantOnly?: boolean;
  /** The user message the new reply answers (assistant-only retry). */
  answeringUserId?: string | null;
  fromComposer?: boolean;
  /** Put the draft back into the composer; true when it did. */
  restore?: () => boolean;
  attachmentOnlyText: string;
  maxMessageFiles: number;
  autoGenerateLabels: boolean;
  restoreOnFailure: boolean;
  /** Run id minted earlier (queued messages). */
  runId?: string;
  skipPrecheck?: boolean;
}

export type SendOutcome = { status: "sent"; runId: string } | { status: "not_sent"; reason: string };

export type { AttachmentFacts };
