/**
 * The chat engine: everything that happens between pressing send and a
 * finished reply, independent of React. It owns the conversations' message
 * data, the optimistic turns, running streams, re-attachment to runs that
 * continued on the server, stop/cancel, the queue of messages typed while a
 * reply runs, and the polling that follows a reply.
 *
 * Components read through `view(key)` (memoised per change) and subscribe with
 * `subscribe(key, listener)`.
 */
import { isAbortError, ApiError } from "@/lib/api/errors";
import type { ActivityLabel, ChatAttachment, ChatMessage, ConversationDTO, MessageAlert, MessageDTO, SendResultDTO } from "../types";
import { TextBuffer, ThinkBuffer, TEXT_FLUSH_MS, THINK_FLUSH_MS } from "../lib/buffers";
import { upstreamSummary } from "../lib/error-text";
import { localAssistantId, localUserId, newRunId } from "../lib/ids";
import { mapMessage, messageKey, EMPTY_USAGE, readDebug } from "../lib/message-map";
import { metadataChanged, nextDelay, shouldPoll, GIVE_UP_MS } from "../lib/metadata-poll";
import {
  editItem,
  enqueue,
  failedChains,
  guideToFront,
  parentRunForNew,
  pickDispatchable,
  pollDelay,
  removeItem,
  RunStatusBook,
  SUCCESS_STATUSES,
  FAILED_STATUSES,
  type ParentOutcome,
  type QueuedMessage,
} from "../lib/queue";
import { ReplayDedupe } from "../lib/replay";
import { buildSendBody, LIMITS, type UiStateEntry } from "../lib/request";
import type { RunStreamHandlers } from "../lib/stream-consume";
import { decideTask, placeholderShape, replyContentType, requestedResponseType, taskEndpoint, type TaskKind } from "../lib/task-decision";
import { optimisticTitle as makeOptimisticTitle, isPlaceholderTitle } from "../lib/text";
import { applyThinkText, mergeTrace, terminalThinkStatus, thinkKey, type LiveThinkEntry } from "../lib/trace";
import { cycleSelection, lastPersistedLeaf, rewriteSelection } from "../lib/tree";
import { derive } from "./engine-merge";
import {
  DRAFT_KEY,
  type AttachState,
  type ChatView,
  type ConvState,
  type EngineDeps,
  type EngineEvents,
  type Listener,
  type SendOutcome,
  type SendParams,
  type Turn,
} from "./engine-types";
import { createStore } from "./store";

const RESUME_POLL_MS = 1500;
const CANCEL_GRACE_MS = 25_000;
const OLDER_PAGE = 30;
const FIRST_PAGE = 30;

type ProcessTrace = NonNullable<ChatMessage["trace"]>;

function emptyState(key: string): ConvState {
  return {
    key,
    server: [],
    total: 0,
    pagesLoaded: 0,
    loading: false,
    loadedOnce: false,
    loadError: null,
    loadingOlder: false,
    turns: [],
    selection: {},
    attach: null,
    version: 0,
    loadSeq: 0,
    signature: "",
  };
}

function summaryFor(error: unknown, fallback: string): { reason: string; status: number; requestId: string; debug: ReturnType<typeof readDebug> } {
  if (error instanceof ApiError) {
    return {
      reason: error.rawMessage || error.message || fallback,
      status: error.status,
      requestId: error.requestId ?? "",
      debug: readDebug(error.details),
    };
  }
  const message = error instanceof Error ? error.message : "";
  return { reason: message || fallback, status: 0, requestId: "", debug: null };
}
