/** Wire types (server DTOs) and view types used across the chat feature. */

export interface ConversationDTO {
  publicID: string;
  projectID: string;
  projectName: string;
  title: string;
  labelsJSON: string;
  model: string;
  isStarred: boolean;
  starredAt: string | null;
  messageCount: number;
  status: string;
  shareStatus: string;
  shareID: string;
  createdAt: string;
  updatedAt: string;
}

export interface ProjectDTO {
  publicID: string;
  name: string;
  description: string;
  systemPrompt: string;
  defaultModel: string;
  mcpDefaultMode: string;
  defaultMCPToolIDs: number[];
  defaultSkillIDs: number[];
  defaultKnowledgeBaseIDs: string[];
  color: string;
  icon: string;
  sortOrder: number;
  status: string;
}

export interface BillingCostDTO {
  billingMode: string;
  billedCurrency: string;
  billedNanousd: number;
  billedUSD: number;
  pricingSnapshotJSON: string;
}

export interface KnowledgeSourceDTO {
  fileName: string;
  fileId: string;
  chunkIndex: number;
  score: number;
  preview: string;
  modality?: string;
}

export interface TraceBlockDTO {
  title?: string;
  summary?: string;
  contentMarkdown?: string;
  status?: string;
  stage?: string;
  roundID?: string;
  parentEventID?: string;
  startedAt?: string | null;
  updatedAt?: string;
  payloadJSON?: string;
}

export interface TraceEventDTO {
  eventID: string;
  eventType?: string;
  phase?: string;
  stage?: string;
  roundID?: string;
  parentEventID?: string;
  title?: string;
  summary?: string;
  contentMarkdown?: string;
  status?: string;
  seq?: number;
  startedAt?: string;
  endedAt?: string | null;
  updatedAt?: string;
  payloadJSON?: string;
}

export interface PromptBlockDTO {
  kind: string;
  title: string;
  tokenEstimate: number;
  cacheable: boolean;
  sourceCount: number;
}

export interface PromptTraceDTO {
  mode?: string;
  statefulUsed?: boolean;
  statefulDisabledReason?: string;
  totalTokenEstimate?: number;
  sentTokenEstimate?: number;
  fullMessageCount?: number;
  sentMessageCount?: number;
  statefulSavedMessages?: number;
  statefulSavedTokens?: number;
  blocks?: PromptBlockDTO[];
}

export interface ProcessTraceDTO {
  enabled?: boolean;
  status?: string;
  process?: TraceBlockDTO | null;
  tools?: TraceBlockDTO | null;
  upstreamThink?: TraceBlockDTO | null;
  promptTrace?: PromptTraceDTO | null;
  events?: TraceEventDTO[];
  detailOmitted?: boolean;
}

export interface ModerationInfoDTO {
  state?: string;
  direction?: string;
  eventID?: string;
  categories?: string[];
}

export interface MessageDTO {
  id: number;
  publicID: string;
  parentMessageID: number | null;
  parentPublicID: string;
  sourcePublicID: string;
  runID: string;
  role: string;
  contentType: string;
  content: string;
  branchReason: string;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  reasoningTokens: number;
  reasoningEffort: string | null;
  latencyMS: number;
  status: string;
  errorCode: string;
  errorMessage: string;
  errorRequestID: string;
  attachments: string;
  platformModelName: string;
  myFeedback: string;
  thumbsUpCount: number;
  thumbsDownCount: number;
  uiState?: Record<string, unknown>;
  billingCost?: BillingCostDTO | null;
  knowledgeSources?: KnowledgeSourceDTO[];
  processTrace?: ProcessTraceDTO | null;
  moderation?: ModerationInfoDTO | null;
  editedAt: string | null;
  createdAt: string;
  updatedAt: string;
}

export interface SendResultDTO {
  userMessage: MessageDTO | null;
  assistantMessage: MessageDTO | null;
  metadataRefreshHint?: string;
}

export interface RunDTO {
  runID: string;
  platformModelName: string;
  status: string;
}

// --- models -----------------------------------------------------------------

export interface ControlOptionDTO {
  value: string;
  label: string;
  description?: string;
}

export interface ControlDTO {
  id: string;
  kind: string;
  type: string;
  label: string;
  description: string;
  icon: string;
  placement: string;
  default: string | null;
  locked: boolean;
  options: ControlOptionDTO[] | null;
  min: number | null;
  max: number | null;
  step: number | null;
  integer: boolean;
  protocols: string[] | null;
}

export interface PricingTierDTO {
  fromTokens: number;
  upToTokens: number | null;
  inputUSDPerMTokens: number;
  cacheReadUSDPerMTokens: number;
  cacheWriteUSDPerMTokens: number;
  outputUSDPerMTokens: number;
}

export interface PricingPeriodDTO {
  name: string;
  weekdays: number[];
  start: string;
  end: string;
  ratePercent: number;
}

export interface PricingDTO {
  currency: string;
  isFree: boolean;
  mode: string;
  inputUSDPerMTokens: number;
  cacheReadUSDPerMTokens: number;
  cacheWriteUSDPerMTokens: number;
  cacheWrite5mMultiplier?: number;
  cacheWrite1hMultiplier?: number;
  outputUSDPerMTokens: number;
  callUSDPerCall: number;
  durationUSDPerSecond: number;
  tiers: PricingTierDTO[] | null;
  schedulePeriods: PricingPeriodDTO[] | null;
  scheduleUTCOffsetMinutes: number;
}

export interface ModelDTO {
  platformModelName: string;
  vendor: string;
  vendorName: string;
  vendorIcon: string;
  displayGroupID: number | null;
  displayGroupName: string;
  displayGroupIcon: string;
  kindsJSON: string;
  icon: string;
  protocolsJSON: string;
  capabilitiesJSON: string;
  description: string;
  sortOrder: number;
  pricing: PricingDTO | null;
  reasoning: { levels: string[]; default: string; controlPath: string | null; locked: boolean } | null;
  controls: ControlDTO[] | null;
  inputModalities: string[] | null;
  outputModalities: string[] | null;
  contextWindow: number | null;
  source: string;
  providerName: string;
  providerIcon: string;
}

export interface ModelOptionPolicyDTO {
  mode: string;
  allowedPathsJSON: string;
  deniedPathsJSON: string;
  nativeTools: unknown[];
}

// --- composer resources -----------------------------------------------------

export interface McpToolDTO {
  id: number;
  serverID: number;
  serverName: string;
  name: string;
  displayName: string;
  description: string;
  attachmentInputMode: string;
  priceNanousd: number;
  status: string;
}

export interface SkillSummaryDTO {
  id: number;
  scope: string;
  title: string;
  trigger: string;
  description: string;
  enabled: boolean;
  sortOrder: number;
}

export interface PromptPresetDTO {
  id: number;
  scope: string;
  title: string;
  trigger: string;
  description: string;
  content: string;
}

export interface KnowledgeBaseDTO {
  publicID: string;
  scope: string;
  name: string;
  description: string;
  enabled: boolean;
  fileCount: number;
  readyFileCount: number;
  processingFileCount: number;
}

export interface UiComponentDTO {
  id: number;
  scope: string;
  name: string;
  version: number;
  description: string;
  rendererKind: string;
  enabled: boolean;
  sortOrder: number;
}

export interface FileDTO {
  fileID: string;
  fileName: string;
  mimeType: string;
  detectedMIME: string;
  fileCategory: string;
  sizeBytes: number;
  status: string;
  processingStatus: string;
  processingReady: boolean;
  processingErrorCode: string;
  processingErrorMessage: string;
  extractStatus: string;
  embedStatus: string;
  embedError: string;
  chunkCount: number;
  ragOptOut: boolean;
  canVectorize: boolean;
  vectorizationReason: string;
}

export interface FilePolicyDTO {
  maxMessageFiles: number;
  maxUploadFileBytes: number;
  allowedMIMETypes: string[] | null;
  imageMaxBytes: number;
  docMaxBytes: number;
  effectiveImageMaxBytes: number;
  effectiveDocMaxBytes: number;
  fullContextMaxBytes: number;
  fullContextMaxTokens: number;
  fullContextPDFMaxPages: number;
  ragAvailable: boolean;
  ragAvailabilityReason: string;
  capabilityMode: string;
  fileMode: string;
}

// --- view types ---------------------------------------------------------------

export type MessageRole = "user" | "assistant" | "system";
export type MessageStatus = "pending" | "success" | "error" | "interrupted" | "canceled" | "blocked";
export type BranchReason = "default" | "retry" | "edit";

export interface ChatAttachment {
  fileId: string;
  fileName: string;
  mimeType: string;
  detectedMime: string;
  fileCategory: string;
  fileSize: number;
  durationSeconds: number;
  kind: "image" | "file";
  processingStatus: string;
  processingReady: boolean;
  processingErrorCode: string;
  processingErrorMessage: string;
  /** Local preview URL (blob:), never persisted. */
  previewUrl?: string;
  /** Temporary chat only: the file itself, never persisted or uploaded to storage. */
  file?: File;
  embedStatus?: string;
  extractStatus?: string;
  ragOptOut?: boolean;
  ragReady?: boolean;
}

export interface KnowledgeSource {
  fileName: string;
  fileId: string;
  chunkIndex: number;
  score: number;
  preview: string;
  modality: "text" | "image";
}

export interface MessageAlert {
  kind: "blocked" | "interrupted";
  title: "blocked" | "interrupted";
  lines: { key: "description" | "eventId" | "categories" | "billed"; value?: string }[];
  message?: string;
  errorCode?: string;
  errorId?: string;
  debug?: DebugSnapshot | null;
}

export interface DebugSnapshot {
  method: string;
  path: string;
  status: number;
  requestHeaders: Record<string, string>;
  requestBody: string;
  responseHeaders: Record<string, string>;
  responseBody: string;
}

export interface UsageCounters {
  input: number;
  output: number;
  cacheRead: number;
  cacheWrite: number;
  reasoning: number;
}

export interface ActivityLabel {
  kind: "file_proc" | "rag_search" | "moderation" | "media" | "image";
  /** Server wording, when it sent any; the UI falls back to its own text. */
  message?: string;
  status?: string;
}

/** What is known only while a reply is being produced in this browser. */
export interface LiveInfo {
  /** True while this browser streams (or has re-attached to) the reply. */
  streaming: boolean;
  label: ActivityLabel | null;
  startedAt: number;
  shape?: "wide" | "portrait" | "square";
  /** Latest partial image, as a data URL. */
  preview?: string;
}

export interface ChatMessage {
  /** Stable row key (role + run id when a run exists). */
  key: string;
  /** Public id, or a local id while the turn is optimistic. */
  id: string;
  /** Numeric server id, null for local messages. */
  numericId: number | null;
  persisted: boolean;
  role: MessageRole;
  parentId: string | null;
  sourceId: string | null;
  branchReason: BranchReason;
  runId: string;
  content: string;
  contentType: string;
  status: MessageStatus;
  attachments: ChatAttachment[];
  usage: UsageCounters;
  reasoningEffort: string | null;
  latencyMs: number;
  model: string;
  errorCode: string;
  errorMessage: string;
  errorRequestId: string;
  feedback: "up" | "down" | "";
  editedAt: string | null;
  createdAt: string;
  updatedAt: string;
  billing: BillingCostDTO | null;
  sources: KnowledgeSource[];
  trace: ProcessTraceDTO | null;
  moderation: ModerationInfoDTO | null;
  uiState: Record<string, Record<string, unknown>>;
  alert: MessageAlert | null;
  /** Client-only: compaction notice remembered from `compact_done`. */
  compaction?: { method: string; freedTokens: number; keptTurns: number; summaryPreview: string } | null;
  /** True for the local optimistic copy. */
  optimistic?: boolean;
  live?: LiveInfo;
}
