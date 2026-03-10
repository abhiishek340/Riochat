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
