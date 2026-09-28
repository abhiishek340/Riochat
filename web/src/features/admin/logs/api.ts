import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { readPage, type PageResult } from "../lib/paging";
import type { UsageCounts } from "./breakdown";

type Query = Record<string, string | number | boolean | undefined>;

async function paged<T>(path: string, query: Query, signal?: AbortSignal): Promise<PageResult<T>> {
  return readPage<T>(await api.get<unknown>(path, { signal, query }));
}

export interface AuditLog {
  id: number;
  requestID: string;
  actorUserID: number;
  actorUsername: string;
  actorDisplayName: string;
  actorLabel: string;
  action: string;
  resource: string;
  resourceID: string;
  ip: string;
  userAgent: string;
  detailJSON: string;
  createdAt: string;
}

export interface AuthEvent {
  id: number;
  requestID: string;
  userID: number;
  username: string;
  userDisplayName: string;
  userLabel: string;
  eventType: string;
  result: string;
  reason: string;
  clientIP: string;
  userAgent: string;
  detailJSON: string;
  occurredAt: string;
}

export interface CallLog extends UsageCounts {
  id: number;
  userID: number;
  username: string;
  userDisplayName: string;
  userLabel: string;
  conversationID: number;
  platformModelName: string;
  upstreamName: string;
  upstreamModelName: string;
  routedBindingCode: string;
  providerProtocol: string;
  latencyMS: number;
  billedUSD: number;
  billedNanousd: number;
  billedCurrency: string;
  balanceAfterUSD: number | null;
  isFreeModel: boolean;
  serviceTier: string;
  usageSpeed: string;
  usageDate: string;
  pricingSnapshotJSON: string;
  modelVendor?: string;
  modelIcon?: string;
  createdAt: string;
}

export interface PaymentOrder {
  id: number;
  orderNo: string;
  orderType: string;
  provider: string;
  status: string;
  userID: number;
  username: string;
  userDisplayName: string;
  userLabel: string;
  planID: number;
  priceID: number;
  billingInterval: string;
  cycles: number;
  baseAmountCents: number;
  baseCurrency: string;
  payAmountCents: number;
  payCurrency: string;
  fxRate: string;
  creditUSD: number;
  externalCheckoutID: string;
  externalPaymentID: string;
  paidAt: string | null;
  expiredAt: string | null;
  snapshotJSON: string;
  createdAt: string;
}

export interface RedemptionRecord {
  id: number;
  codeID: number;
  codeHint: string;
  codeStatus: string;
  codeDescription: string;
  userID: number;
  username: string;
  userDisplayName: string;
  userLabel: string;
  mode: string;
  rewardType: string;
  creditUSD: number;
  durationDays: number;
  planID: number;
  planName: string;
  balanceBeforeNanousd: number | null;
  balanceAfterNanousd: number | null;
  subscriptionID: number;
  refNo: string;
  snapshotJSON: string;
  createdAt: string;
}

export interface ConversationEvent {
  id: number;
  eventID: string;
  runID: string;
  roundID: string;
  parentEventID: string;
  seq: number;
  conversationID: number;
  userID: number;
  username: string;
  userDisplayName: string;
  userLabel: string;
  eventScope: string;
  eventType: string;
  phase: string;
  stage: string;
  status: string;
  title: string;
  summary: string;
  contentMarkdown: string;
  toolName: string;
  toolCallID: string;
  platformModelName: string;
  upstreamName: string;
  upstreamModelName: string;
  providerProtocol: string;
  routedBindingCode: string;
  startedAt: string;
  endedAt: string | null;
  latencyMS: number;
  inputJSON: string;
  inputOmitted: boolean;
  inputSizeBytes: number;
  outputJSON: string;
  outputOmitted: boolean;
  outputSizeBytes: number;
  payloadJSON: string;
  payloadOmitted: boolean;
  payloadSizeBytes: number;
  errorJSON: string;
  errorOmitted: boolean;
  errorSizeBytes: number;
  createdAt: string;
}

export interface ErrorLog {
  id: number;
  requestID: string;
  traceID: string;
  userID: number;
  userUsername: string;
  userDisplayName: string;
  userLabel: string;
  method: string;
  route: string;
  path: string;
  statusCode: number;
  errorCode: string;
  cause: string;
  detail: string;
  latencyMS: number;
  clientIP: string;
  userAgent: string;
  createdAt: string;
}

export const listAudit = (query: Query, signal?: AbortSignal) => paged<AuditLog>("/admin/audit-logs", query, signal);
export const listAuthEvents = (query: Query, signal?: AbortSignal) => paged<AuthEvent>("/admin/user-auth-events", query, signal);
export const listCalls = (query: Query, signal?: AbortSignal) => paged<CallLog>("/admin/call-logs", query, signal);
export const listOrders = (query: Query, signal?: AbortSignal) => paged<PaymentOrder>("/admin/payment-orders", query, signal);
export const listRedemptions = (query: Query, signal?: AbortSignal) => paged<RedemptionRecord>("/admin/redemptions", query, signal);
export const listConversationEvents = (query: Query, signal?: AbortSignal) => paged<ConversationEvent>("/admin/conversation-events", query, signal);
export const listErrors = (query: Query, signal?: AbortSignal) => paged<ErrorLog>("/admin/error-logs", query, signal);

export function getConversationEvent(id: number, signal?: AbortSignal): Promise<ConversationEvent> {
  return api.get<ConversationEvent | { event: ConversationEvent }>(`/admin/conversation-events/${seg(id)}`, { signal }).then((out) => ("event" in out ? out.event : out));
}

export interface CleanupResult {
  type: string;
  before: string;
  deletedCount: number;
}

export function cleanupLogs(body: { type: string; before: string }): Promise<CleanupResult> {
  return api.post<CleanupResult>("/admin/logs/cleanup", body);
}
