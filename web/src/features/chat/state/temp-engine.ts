/**
 * Temporary chat: a conversation that exists only in this tab. The server keeps
 * nothing; the whole local history is sent with every request. Leaving the chat
 * discards messages, retained files and live traces.
 */
import { api, resolveAccessToken } from "@/lib/api/client";
import { ApiError, isAbortError } from "@/lib/api/errors";
import type { ChatAttachment, ChatMessage, MessageAlert, SendResultDTO } from "../types";
import { TextBuffer, TEXT_FLUSH_MS } from "../lib/buffers";
import { newRunId, newSessionId } from "../lib/ids";
import { EMPTY_USAGE, mapMessage, messageKey, readDebug } from "../lib/message-map";
import { buildTempRequest, tempFormData, type Selections } from "../lib/request";
import { consumeRunStream, type RunStreamHandlers } from "../lib/stream-consume";
import { decideTask, requestedResponseType, type ModelFacts } from "../lib/task-decision";
import { applyThinkText, mergeTrace, thinkKey, type LiveThinkEntry } from "../lib/trace";
import type { Options } from "../lib/options";
import type { ControlChoices } from "../lib/controls";
import { createStore } from "./store";
import type { ChatView, Notice } from "./engine-types";
import { TEMP_KEY } from "./engine-types";
import { UI_BLOCK_VERSION } from "../lib/request";

export interface TempSendParams {
  text: string;
  attachments: ChatAttachment[];
  model: string;
  modelFacts: ModelFacts;
  options: Options;
  optionPolicyMode?: string;
  controls?: ControlChoices;
  selections: Selections;
  attachmentOnlyText: string;
  omittedNote: string;
  attachmentNote: (names: string[]) => string;
  /** Shown when there is no session token to send the request with. */
  sessionExpiredText: string;
  /** Shown in place of the reply when the user stops it before any text arrived. */
  stoppedText: string;
  restore?: () => boolean;
}

let counter = 0;
