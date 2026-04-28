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

export class TempEngine {
  private messages: ChatMessage[] = [];
  private listeners = new Set<() => void>();
  private controller: AbortController | null = null;
  private sessionId: string | null = null;
  private cached: { version: number; view: ChatView } | null = null;
  private version = 0;
  private buffer = new TextBuffer();
  private timer: ReturnType<typeof setTimeout> | null = null;
  private noticeListeners = new Set<(n: Notice) => void>();
  readonly liveThinking = createStore<Record<string, LiveThinkEntry[]>>({});
  /** UI block states per message id, kept for the next turn (memory only). */
  readonly uiStates = new Map<string, Record<string, Record<string, unknown>>>();

  subscribe(listener: () => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  onNotice(listener: (n: Notice) => void): () => void {
    this.noticeListeners.add(listener);
    return () => this.noticeListeners.delete(listener);
  }

  private notice(n: Notice): void {
    for (const l of [...this.noticeListeners]) l(n);
  }

  private bump(): void {
    this.version++;
    for (const l of [...this.listeners]) l();
  }

  get running(): boolean {
    return this.controller !== null;
  }

  get hasMessages(): boolean {
    return this.messages.length > 0;
  }

  view(): ChatView {
    if (this.cached && this.cached.version === this.version) return this.cached.view;
    const last = this.messages[this.messages.length - 1];
    const view: ChatView = {
      key: TEMP_KEY,
      messages: this.messages,
      path: this.messages,
      navigators: new Map(),
      selection: {},
      loading: false,
      loadError: null,
      loadedOnce: true,
      hasOlder: false,
      loadingOlder: false,
      busy: this.running,
      streamingHere: this.running,
      hasContent: this.messages.length > 0,
      runIds: [...new Set(this.messages.map((m) => m.runId).filter(Boolean))],
    };
    void last;
    this.cached = { version: this.version, view };
    return view;
  }

  private make(role: "user" | "assistant", partial: Partial<ChatMessage>): ChatMessage {
    const id = `local-t-${++counter}`;
    const now = new Date().toISOString();
    const runId = partial.runId ?? "";
    return {
      key: messageKey(role, runId, id),
      id,
      numericId: null,
      persisted: false,
      role,
      parentId: null,
      sourceId: null,
      branchReason: "default",
      runId,
      content: "",
      contentType: role === "user" ? "text" : "markdown",
      status: "success",
      attachments: [],
      usage: { ...EMPTY_USAGE },
      reasoningEffort: null,
      latencyMs: 0,
      model: "",
      errorCode: "",
      errorMessage: "",
      errorRequestId: "",
      feedback: "",
      editedAt: null,
      createdAt: now,
      updatedAt: now,
      billing: null,
      sources: [],
      trace: null,
      moderation: null,
      uiState: {},
      alert: null,
      optimistic: true,
      ...partial,
    };
  }

  private patchLast(patch: Partial<ChatMessage>): void {
    const i = this.messages.length - 1;
    const m = this.messages[i];
    if (!m) return;
    this.messages = [...this.messages.slice(0, i), { ...m, ...patch }];
    this.bump();
  }

  private flush(now = false): void {
    if (now && this.timer) {
      clearTimeout(this.timer);
      this.timer = null;
    }
    if (!this.buffer.hasPending) return;
    const { replace, append } = this.buffer.take();
    const last = this.messages[this.messages.length - 1];
    if (!last) return;
    const content = (replace ?? last.content) + append;
    this.patchLast({ content, live: content && last.live ? { ...last.live, label: null } : last.live });
  }

  /** Sends a new turn; history is everything before it. */
  async send(p: TempSendParams): Promise<boolean> {
    if (this.running) {
      this.notice({ code: "concurrency" });
      return false;
    }
    const text = p.text.trim();
    if (!text && p.attachments.length === 0) return false;
    if (!p.model) {
      this.notice({ code: "noModel" });
      return false;
    }
    const decision = decideTask({
      model: p.modelFacts,
      attachments: p.attachments.map((a) => ({ fileCategory: a.fileCategory, mimeType: a.mimeType, detectedMime: a.detectedMime })),
      requested: requestedResponseType(p.options),
    });
    if (decision.blocked || decision.task !== "chat") {
      this.notice({ code: "blocked", block: decision.blocked ?? "B9" });
      return false;
    }
    const content = text || p.attachmentOnlyText;
    const runId = newRunId();
    const history = this.messages
      .filter((m) => m.role === "user" || m.role === "assistant")
      .map((m) => ({ role: m.role as "user" | "assistant", content: m.content, files: m.attachments.map((a) => a.file).filter((f): f is File => !!f) }));
    const now = Date.now();
    const user = this.make("user", { runId, content, attachments: p.attachments, contentType: p.attachments.length ? "mixed" : "text", createdAt: new Date(now).toISOString() });
    const assistant = this.make("assistant", {
      runId,
      status: "pending",
      model: p.model,
      parentId: user.id,
      live: { streaming: true, label: null, startedAt: now },
    });
    this.messages = [...this.messages, { ...user, parentId: this.messages[this.messages.length - 1]?.id ?? null }, assistant];
    this.sessionId ??= newSessionId();
    this.controller = new AbortController();
    const signal = this.controller.signal;
    this.bump();

    const uiStates = this.collectUiStates();
    const req = buildTempRequest({
      sessionId: this.sessionId,
      clientRunId: runId,
      model: p.model,
      options: p.options,
      optionPolicyMode: p.optionPolicyMode,
      controls: p.controls,
      selections: p.selections,
      history,
      turn: { role: "user", content, files: p.attachments.map((a) => a.file).filter((f): f is File => !!f) },
      uiStates,
      omittedNote: p.omittedNote,
      attachmentNote: p.attachmentNote,
    });
    void UI_BLOCK_VERSION;

    const handlers = this.handlers(runId);
    try {
      if (!(await resolveAccessToken())) throw new Error(p.sessionExpiredText);
      const body = req.files.length > 0 ? tempFormData(req) : req.payload;
      const response = await api.raw("/temporary-chat/messages/stream", { method: "POST", body, signal, headers: { Accept: "application/x-ndjson" } });
      const outcome = await consumeRunStream(response, handlers);
      this.flush(true);
      if (outcome.kind === "completed" || outcome.kind === "interrupted") this.settle(outcome.data, outcome.kind === "interrupted" ? outcome.error.message : "");
      else if (outcome.kind === "blocked") {
        const alert: MessageAlert = {
          kind: "blocked",
          title: "blocked",
          lines: [{ key: "description" }, ...(outcome.event.billedReason === "moderation_blocked_upstream_usage" ? [{ key: "billed" as const }] : [])],
        };
        this.patchLast({ status: "blocked", content: "", alert, live: undefined });
        this.notice({ code: "moderation" });
      }
    } catch (error) {
      this.flush(true);
      if (isAbortError(error) || signal.aborted) {
        // Partial text stays; with none, the row says the generation was stopped.
        const last = this.messages[this.messages.length - 1];
        const alert: MessageAlert | null = last && !last.content.trim() ? { kind: "interrupted", title: "interrupted", lines: [], message: p.stoppedText } : null;
        this.patchLast({ status: "canceled", live: undefined, ...(alert ? { alert } : {}) });
      } else this.fail(error, p);
    } finally {
      this.controller = null;
      this.bump();
    }
    return true;
  }

  private collectUiStates() {
    const out: { messageIndex: number; blockID: string; title?: string; state: Record<string, unknown> }[] = [];
    this.messages.forEach((m, i) => {
      if (m.role !== "assistant") return;
      const blocks = this.uiStates.get(m.id);
      if (!blocks) return;
      for (const [blockID, state] of Object.entries(blocks)) if (Object.keys(state).length > 0) out.push({ messageIndex: i, blockID, state });
    });
    return out.slice(-32);
  }

  private handlers(runId: string): RunStreamHandlers {
    return {
      onDelta: (text) => {
        this.buffer.push(text);
        if (!this.timer) {
          this.timer = setTimeout(() => {
            this.timer = null;
            this.flush();
          }, TEXT_FLUSH_MS);
        }
      },
      onSnapshot: (text) => {
        this.buffer.replace(text);
        this.flush(true);
      },
      onFileProc: (message) => this.patchLive({ kind: "file_proc", message }),
      onRagSearch: (message) => this.patchLive({ kind: "rag_search", message }),
      onModerationChecking: () => this.patchLive({ kind: "moderation" }),
      onUsage: (u) => {
        const last = this.messages[this.messages.length - 1];
        if (!last) return;
        const take = (n: number | undefined, old: number) => (typeof n === "number" && n > 0 ? n : old);
        this.patchLast({
          usage: {
            input: take(u.input_tokens, last.usage.input),
            output: take(u.output_tokens, last.usage.output),
            cacheRead: take(u.cache_read_tokens, last.usage.cacheRead),
            cacheWrite: take(u.cache_write_tokens, last.usage.cacheWrite),
            reasoning: take(u.reasoning_tokens, last.usage.reasoning),
          },
        });
      },
      onProcess: (e) => {
        const last = this.messages[this.messages.length - 1];
        if (last && e.trace && typeof e.trace === "object") this.patchLast({ trace: mergeTrace(last.trace, e.trace as NonNullable<ChatMessage["trace"]>) });
      },
      onThink: (e) => {
        const key = thinkKey(e);
        const meta = { title: e.title, summary: e.summary, status: e.status, startedAt: e.startedAt, endedAt: e.endedAt, roundId: e.roundID };
        const entries = this.liveThinking.get()[runId] ?? [];
        const next =
          typeof e.contentMarkdown === "string" && e.contentMarkdown
            ? applyThinkText(entries, key, e.contentMarkdown, "replace", meta)
            : applyThinkText(entries, key, e.delta ?? "", "append", meta);
        this.liveThinking.set((s) => ({ ...s, [runId]: next }));
        this.bump();
      },
    };
  }

  private patchLive(label: NonNullable<ChatMessage["live"]>["label"]): void {
    const last = this.messages[this.messages.length - 1];
    if (last?.live) this.patchLast({ live: { ...last.live, label } });
  }

  private settle(data: SendResultDTO, interruptedReason: string): void {
    const dto = data.assistantMessage;
    const last = this.messages[this.messages.length - 1];
    if (!dto || !last) return;
    const mapped = mapMessage({ ...dto, publicID: last.id, runID: last.runId });
    this.patchLast({
      content: mapped.content || last.content,
      status: interruptedReason ? "interrupted" : mapped.status,
      usage: mapped.usage.input || mapped.usage.output ? mapped.usage : last.usage,
      latencyMs: mapped.latencyMs,
      live: undefined,
      alert: interruptedReason ? { kind: "interrupted", title: "interrupted", lines: [], message: interruptedReason } : mapped.alert,
      trace: mapped.trace ?? last.trace,
    });
  }

  private fail(error: unknown, p: TempSendParams): void {
    const message = error instanceof Error ? error.message : "";
    const alert: MessageAlert = {
      kind: "interrupted",
      title: "interrupted",
      lines: [],
      message,
      errorCode: error instanceof ApiError ? error.errorCode : undefined,
      errorId: error instanceof ApiError ? error.requestId : undefined,
      debug: error instanceof ApiError ? readDebug(error.details) : null,
    };
    // The partial text stays in the history for later turns.
    this.patchLast({ status: "error", live: undefined, alert, errorMessage: message });
    p.restore?.();
    this.notice({ code: "sendFailed", error });
  }

  stop(): void {
    this.controller?.abort();
  }

  /** Drops this message and everything after it. */
  truncateBefore(messageId: string): ChatMessage[] {
    const at = this.messages.findIndex((m) => m.id === messageId);
    if (at < 0) return [];
    const removed = this.messages.slice(at);
    this.messages = this.messages.slice(0, at);
    this.bump();
    return removed;
  }

  /** Keeps the message, drops what follows. */
  truncateAfter(messageId: string): void {
    const at = this.messages.findIndex((m) => m.id === messageId);
    if (at < 0) return;
    this.messages = this.messages.slice(0, at + 1);
    this.bump();
  }

  editLocal(messageId: string, content: string, markEdited: boolean): void {
    this.messages = this.messages.map((m) => (m.id === messageId ? { ...m, content, editedAt: markEdited ? new Date().toISOString() : m.editedAt } : m));
    this.bump();
  }

  patch(messageId: string, patch: Partial<ChatMessage>): void {
    this.messages = this.messages.map((m) => (m.id === messageId ? { ...m, ...patch } : m));
    this.bump();
  }

  /** Forget everything: leaving the chat. */
  discard(): void {
    this.controller?.abort();
    this.controller = null;
    for (const m of this.messages) for (const a of m.attachments) if (a.previewUrl) URL.revokeObjectURL(a.previewUrl);
    this.messages = [];
    this.sessionId = null;
    this.uiStates.clear();
    this.liveThinking.set({});
    this.buffer = new TextBuffer();
    this.bump();
  }
}

export const tempEngine = new TempEngine();
