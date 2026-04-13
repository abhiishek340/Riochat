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

export class ChatEngine {
  private states = new Map<string, ConvState>();
  private listeners = new Map<string, Set<() => void>>();
  private anyListeners = new Set<() => void>();
  private events = new Map<string, Set<Listener<never>>>();
  private views = new Map<string, { version: number; view: ChatView }>();

  /** Streams running in this browser, by run id. */
  private active = new Map<string, Turn>();
  private cursors = new Map<string, number>();
  /** The reply text at the moment a stream was lost, so a re-attach can tell repeats from news. */
  private cursorText = new Map<string, string>();
  private order = 0;
  private visibleKey: string | null = null;
  private pendingTimers = new Map<string, ReturnType<typeof setTimeout>>();
  private metaPolls = new Set<string>();
  private lastModelRun = new Map<string, number>();
  private lastAttachEnd = new Map<string, number>();

  // thinking text kept per run, outside message data
  readonly liveThinking = createStore<Record<string, LiveThinkEntry[]>>({});
  private thinkBuffers = new Map<string, ThinkBuffer>();
  private thinkTimers = new Map<string, ReturnType<typeof setTimeout>>();
  private textBuffers = new Map<string, TextBuffer>();
  private textTimers = new Map<string, ReturnType<typeof setTimeout>>();

  // queue
  readonly queueStore = createStore<QueuedMessage[]>([]);
  private queuedParams = new Map<string, SendParams>();
  private statusBook = new RunStatusBook();
  private statusTimer: ReturnType<typeof setTimeout> | null = null;
  private statusFailures = 0;
  private pumping = false;

  constructor(private deps: EngineDeps) {
    deps.runs.subscribe((_id, running) => {
      if (!running) this.pump();
    });
  }

  // --- events ----------------------------------------------------------------------

  on<K extends keyof EngineEvents>(type: K, listener: Listener<EngineEvents[K]>): () => void {
    let set = this.events.get(type);
    if (!set) this.events.set(type, (set = new Set()));
    set.add(listener as Listener<never>);
    return () => set.delete(listener as Listener<never>);
  }

  private emit<K extends keyof EngineEvents>(type: K, payload: EngineEvents[K]): void {
    const set = this.events.get(type);
    if (!set) return;
    for (const l of [...set]) (l as Listener<EngineEvents[K]>)(payload);
  }

  // --- subscription ---------------------------------------------------------------

  subscribe(key: string, listener: () => void): () => void {
    let set = this.listeners.get(key);
    if (!set) this.listeners.set(key, (set = new Set()));
    set.add(listener);
    return () => set.delete(listener);
  }

  subscribeAny(listener: () => void): () => void {
    this.anyListeners.add(listener);
    return () => this.anyListeners.delete(listener);
  }

  private bump(st: ConvState): void {
    st.version++;
    for (const l of [...(this.listeners.get(st.key) ?? [])]) l();
    for (const l of [...this.anyListeners]) l();
  }

  private ensure(key: string): ConvState {
    let st = this.states.get(key);
    if (!st) this.states.set(key, (st = emptyState(key)));
    return st;
  }

  /** The memoised, derived state of one conversation. */
  view(key: string): ChatView {
    const st = this.ensure(key);
    const hit = this.views.get(key);
    if (hit && hit.version === st.version) return hit.view;
    const d = derive(st);
    const last = d.path[d.path.length - 1];
    const runningHere = st.turns.some((t) => t.stage === "running" && d.path.some((m) => m.runId === t.runId));
    const busy = !!last && last.role === "assistant" && last.status === "pending";
    const view: ChatView = {
      key,
      messages: d.messages,
      path: d.path,
      navigators: d.navigators,
      selection: d.selection,
      loading: st.loading && !st.loadedOnce,
      loadError: st.loadError,
      loadedOnce: st.loadedOnce,
      hasOlder: st.server.length < st.total && st.server.some((m) => m.numericId !== null),
      loadingOlder: st.loadingOlder,
      busy: busy || runningHere,
      streamingHere: runningHere || (!!st.attach && d.path.some((m) => m.runId === st.attach?.runId)),
      hasContent: d.path.length > 0 || st.turns.some((t) => t.stage === "running"),
      runIds: [...new Set(d.path.map((m) => m.runId).filter(Boolean))],
    };
    this.views.set(key, { version: st.version, view });
    return view;
  }

  setVisible(key: string | null): void {
    if (this.visibleKey === key) return;
    const prev = this.visibleKey;
    this.visibleKey = key;
    if (prev) {
      const st = this.states.get(prev);
      if (st?.attach) this.detach(st);
      this.clearPendingTimer(prev);
    }
    if (key) this.syncPending(key);
  }

  // --- loading ---------------------------------------------------------------------

  async load(key: string, options: { reload?: boolean } = {}): Promise<void> {
    if (key === DRAFT_KEY) return;
    const st = this.ensure(key);
    const seq = ++st.loadSeq;
    if (!st.loadedOnce || !options.reload) {
      st.loading = true;
      if (!options.reload) st.loadedOnce = false;
    }
    st.loadError = null;
    this.bump(st);
    try {
      const res = await this.deps.messages.list(key, { page: 1, pageSize: FIRST_PAGE, tail: true, trace: "summary" });
      if (seq !== st.loadSeq) return;
      const fresh = res.results.map(mapMessage);
      const firstId = fresh.reduce<number | null>((min, m) => (m.numericId !== null && (min === null || m.numericId < min) ? m.numericId : min), null);
      if (options.reload && st.pagesLoaded > 1 && firstId !== null) {
        const older = st.server.filter((m) => m.numericId !== null && m.numericId < firstId);
        st.server = [...older, ...fresh];
      } else {
        st.server = fresh;
        st.pagesLoaded = 1;
      }
      st.total = res.total;
      st.loading = false;
      st.loadedOnce = true;
      this.bump(st);
      this.dropPersistedTurns(st);
      this.syncPending(key);
      this.pump();
    } catch (error) {
      if (seq !== st.loadSeq) return;
      st.loading = false;
      st.loadedOnce = st.loadedOnce || st.server.length > 0;
      if (!options.reload || st.server.length === 0) st.loadError = error;
      this.bump(st);
    }
  }

  async loadOlder(key: string): Promise<void> {
    const st = this.states.get(key);
    if (!st || st.loadingOlder || st.server.length >= st.total) return;
    const oldest = st.server[0];
    if (!oldest || oldest.numericId === null) return;
    st.loadingOlder = true;
    this.bump(st);
    try {
      const res = await this.deps.messages.list(key, { page: 1, pageSize: OLDER_PAGE, beforeId: oldest.numericId, trace: "summary" });
      const known = new Set(st.server.map((m) => m.id));
      const older = res.results.map(mapMessage).filter((m) => !known.has(m.id));
      st.server = [...older, ...st.server];
      st.total = res.total;
      st.pagesLoaded += 1;
    } catch {
      /* the next scroll tries again */
    } finally {
      st.loadingOlder = false;
      this.bump(st);
    }
  }

  /** Drops settled turns whose messages the server data now contains. */
  private dropPersistedTurns(st: ConvState): void {
    if (st.turns.length === 0) return;
    const keys = new Set(st.server.map((m) => m.key));
    const keep: Turn[] = [];
    let changed = false;
    for (const t of st.turns) {
      const persisted =
        (t.stage === "settled" || t.stage === "failed" || t.stage === "aborted") &&
        keys.has(t.assistant.key) &&
        (t.assistantOnly || !t.user || keys.has(t.user.key)) &&
        (st.server.find((m) => m.key === t.assistant.key)?.status !== "pending" || t.stage === "aborted" || t.stage === "failed");
      if (persisted) {
        this.releaseTurn(t);
        changed = true;
      } else keep.push(t);
    }
    if (changed) {
      st.turns = keep;
      this.bump(st);
    }
  }

  private releaseTurn(t: Turn): void {
    this.liveThinkingKeep(t.runId);
    if (t.restored) return;
    for (const a of t.attachments) if (a.previewUrl) this.deps.revokeUrl(a.previewUrl);
  }

  private liveThinkingKeep(_runId: string): void {
    /* richer live thinking stays available for the run until it is replaced */
  }

  replaceServerMessage(key: string, dto: MessageDTO): void {
    const st = this.states.get(key);
    if (!st) return;
    const mapped = mapMessage(dto);
    st.server = st.server.map((m) => (m.id === mapped.id ? mapped : m));
    this.bump(st);
  }

  removeServerMessage(key: string, id: string): void {
    const st = this.states.get(key);
    if (!st) return;
    st.server = st.server.filter((m) => m.id !== id);
    st.total = Math.max(0, st.total - 1);
    this.bump(st);
  }

  /** Optimistic local patch of one message (feedback, edit). */
  patchMessage(key: string, id: string, patch: Partial<ChatMessage>): void {
    const st = this.states.get(key);
    if (!st) return;
    st.server = st.server.map((m) => (m.id === id ? { ...m, ...patch } : m));
    this.bump(st);
  }

  cycle(key: string, messageId: string, delta: -1 | 1): void {
    const st = this.states.get(key);
    if (!st) return;
    const v = this.view(key);
    st.selection = cycleSelection(v.messages, st.selection, messageId, delta);
    this.bump(st);
  }

  /** Forget everything about a conversation (deleted). */
  forget(key: string): void {
    const st = this.states.get(key);
    if (!st) return;
    this.detach(st);
    this.states.delete(key);
    this.views.delete(key);
    this.clearPendingTimer(key);
  }

  /** Remove a draft's turns and state (new chat). */
  resetDraft(): void {
    const st = this.states.get(DRAFT_KEY);
    if (!st) return;
    if (st.turns.some((t) => t.stage === "running")) return;
    this.states.set(DRAFT_KEY, emptyState(DRAFT_KEY));
    this.views.delete(DRAFT_KEY);
    for (const l of [...(this.listeners.get(DRAFT_KEY) ?? [])]) l();
  }

  // --- helpers on the data ---------------------------------------------------------

  runningCount(): number {
    return this.active.size;
  }

  isStreamingHere(runId: string): boolean {
    return this.active.has(runId);
  }

  private branchHasActiveStream(key: string): boolean {
    const st = this.states.get(key);
    if (!st) return false;
    if (st.attach) return true;
    return st.turns.some((t) => t.stage === "running" && t.branchReason === "default" && !t.assistantOnly);
  }

  private pendingKnown(): Set<string> {
    const out = new Set<string>();
    for (const st of this.states.values()) for (const m of st.server) if (m.role === "assistant" && m.status === "pending" && m.runId) out.add(m.runId);
    for (const id of this.active.keys()) out.delete(id);
    return out;
  }

  private countRunning(includePending: boolean): number {
    return this.active.size + (includePending ? this.pendingKnown().size : 0);
  }

  private notice(code: EngineEvents["notice"]["code"], extra: Omit<EngineEvents["notice"], "code"> = {}): void {
    this.emit("notice", { code, ...extra });
  }

  // --- send --------------------------------------------------------------------------

  async send(p: SendParams): Promise<SendOutcome> {
    const text = p.text.trim();
    if (!text && p.attachments.length === 0) return { status: "not_sent", reason: "empty" };
    if (!p.model) {
      this.notice("noModel");
      return { status: "not_sent", reason: "model" };
    }
    const decision = decideTask({
      model: p.modelFacts,
      attachments: p.attachments.map((a) => ({ fileCategory: a.fileCategory, mimeType: a.mimeType, detectedMime: a.detectedMime })),
      requested: requestedResponseType(p.options),
    });
    if (decision.blocked) {
      this.notice("blocked", { block: decision.blocked });
      return { status: "not_sent", reason: "blocked" };
    }
    const branchSend = p.branchReason !== "default";
    if (!branchSend && this.branchHasActiveStream(p.key)) return { status: "not_sent", reason: "busy" };
    if (this.countRunning(branchSend) >= LIMITS.concurrentRuns) {
      this.notice("concurrency", { count: LIMITS.concurrentRuns });
      return { status: "not_sent", reason: "cap" };
    }
    let attachments = p.attachments;
    const cap = p.maxMessageFiles > 0 ? p.maxMessageFiles : 10;
    if (attachments.length > cap) {
      this.notice("attachmentsTruncated", { count: attachments.length - cap });
      attachments = attachments.slice(0, cap);
    }
    const content = text || p.attachmentOnlyText;
    const runId = p.runId ?? newRunId();
    const task = decision.task;
    const startedAt = this.deps.now();
    const st = this.ensure(p.key);

    const userId = localUserId(runId);
    const user: ChatMessage | null = p.assistantOnly
      ? null
      : this.optimisticMessage({
          role: "user", id: userId, runId, parentId: p.parentId, sourceId: p.sourceId, branchReason: p.branchReason, content, contentType: attachments.length ? "mixed" : "text",
          attachments, model: p.model, startedAt, status: "success",
        });
    const answered = p.assistantOnly ? p.answeringUserId ?? p.parentId : userId;
    const assistant = this.optimisticMessage({
      role: "assistant", id: localAssistantId(runId), runId, parentId: answered, sourceId: p.assistantOnly ? p.sourceId : null,
      branchReason: p.assistantOnly ? p.branchReason : "default", content: "", contentType: replyContentType(task), attachments: [], model: p.model, startedAt, status: "pending",
      live: { streaming: true, label: task === "chat" ? null : { kind: "image" }, startedAt, shape: task === "chat" ? undefined : placeholderShape(p.options) },
    });
    const turn: Turn = {
      runId, key: p.key, conversationId: p.key === DRAFT_KEY ? null : p.key, task, assistantOnly: !!p.assistantOnly, user, assistant, result: null,
      stage: "running", attachments, optimisticTitle: "", controller: new AbortController(), cancelRequested: false, cancelTimer: null, startedAt,
      order: ++this.order, terminal: false, fromComposer: !!p.fromComposer, restored: false, model: p.model, branchReason: p.branchReason,
    };
    st.turns = [...st.turns, turn];
    this.active.set(runId, turn);
    this.thinkClear(runId);
    this.deps.runs.register(runId, turn.conversationId ?? "");
    if (turn.conversationId) this.emit("runStarted", { runId, conversationId: turn.conversationId });
    this.bump(st);

    void this.execute(turn, p, content, attachments);
    return { status: "sent", runId };
  }

  private optimisticMessage(args: {
    role: "user" | "assistant";
    id: string;
    runId: string;
    parentId: string | null;
    sourceId: string | null;
    branchReason: ChatMessage["branchReason"];
    content: string;
    contentType: string;
    attachments: ChatAttachment[];
    model: string;
    startedAt: number;
    status: ChatMessage["status"];
    live?: ChatMessage["live"];
  }): ChatMessage {
    const iso = new Date(args.startedAt).toISOString();
    return {
      key: messageKey(args.role, args.runId, args.id),
      id: args.id,
      numericId: null,
      persisted: false,
      role: args.role,
      parentId: args.parentId,
      sourceId: args.sourceId,
      branchReason: args.branchReason,
      runId: args.runId,
      content: args.content,
      contentType: args.contentType,
      status: args.status,
      attachments: args.attachments,
      usage: { ...EMPTY_USAGE },
      reasoningEffort: null,
      latencyMs: 0,
      model: args.role === "assistant" ? args.model : "",
      errorCode: "",
      errorMessage: "",
      errorRequestId: "",
      feedback: "",
      editedAt: null,
      createdAt: iso,
      updatedAt: iso,
      billing: null,
      sources: [],
      trace: null,
      moderation: null,
      uiState: {},
      alert: null,
      optimistic: true,
      live: args.live,
    };
  }

  private stateOf(turn: Turn): ConvState {
    return this.ensure(turn.key);
  }

  private updateAssistant(turn: Turn, patch: Partial<ChatMessage> | ((m: ChatMessage) => Partial<ChatMessage>)): void {
    const delta = typeof patch === "function" ? patch(turn.assistant) : patch;
    turn.assistant = { ...turn.assistant, ...delta };
    this.bump(this.stateOf(turn));
  }

  private setLabel(turn: Turn, label: ActivityLabel | null): void {
    const live = turn.assistant.live;
    if (!live) return;
    this.updateAssistant(turn, { live: { ...live, label } });
  }

  // --- execution -------------------------------------------------------------------

  private async execute(turn: Turn, p: SendParams, content: string, attachments: ChatAttachment[]): Promise<void> {
    let convId = turn.conversationId;
    let userAborted = false;
    try {
      if (!convId) {
        try {
          const created = await this.deps.conversations.create({ model: p.model, projectID: p.projectId ?? "" });
          convId = created.publicID;
          this.rekey(turn, created);
        } catch (error) {
          this.failCreate(turn, p, error);
          return;
        }
      }
      this.applyOptimisticTitle(convId, content, turn);
      const path = this.view(turn.key).path.filter((m) => m.persisted);
      const uiStates = turn.task === "chat" ? this.deps.collectUiStates(path) : [];
      const body = buildSendBody({
        task: turn.task,
        content,
        attachments,
        model: p.model,
        options: p.options,
        optionPolicyMode: p.optionPolicyMode,
        controls: p.controls,
        clientRunId: turn.runId,
        parentId: p.parentId,
        sourceId: p.sourceId,
        branchReason: p.branchReason,
        selections: p.selections,
        uiStates: uiStates as UiStateEntry[],
      });
      const handlers = this.handlersFor(turn);
      const outcome = await this.deps.streams.post(taskEndpoint(convId, turn.task), body, turn.controller.signal, handlers);
      this.flushText(turn.runId, true);
      this.thinkFlush(turn);
      turn.terminal = outcome.kind !== "ended";
      if (outcome.kind === "completed") this.settle(turn, outcome.data, null, p);
      else if (outcome.kind === "interrupted") this.settle(turn, outcome.data, outcome.error, p);
      else if (outcome.kind === "blocked") this.settleBlocked(turn, outcome.event, p);
    } catch (error) {
      this.flushText(turn.runId, true);
      this.thinkFlush(turn);
      if (isAbortError(error) || turn.controller.signal.aborted) {
        userAborted = true;
        this.finishAborted(turn);
      } else {
        this.failSend(turn, p, error);
      }
    } finally {
      this.active.delete(turn.runId);
      this.clearCancelTimer(turn);
      if (turn.terminal) {
        this.cursors.delete(turn.runId);
        this.cursorText.delete(turn.runId);
        this.deps.runs.finish(turn.runId);
        if (turn.conversationId) this.emit("runFinished", { runId: turn.runId, conversationId: turn.conversationId });
      } else {
        if (this.cursors.has(turn.runId)) this.cursorText.set(turn.runId, turn.assistant.content);
        this.deps.runs.orphan(turn.runId);
      }
      void userAborted;
      this.pump();
    }
  }

  private handlersFor(turn: Turn): RunStreamHandlers {
    const run = turn.runId;
    const buf = this.textBufferOf(run);
    return {
      onSeq: (seq) => this.cursors.set(run, seq),
      onFileProc: (message) => this.setLabel(turn, { kind: "file_proc", message }),
      onRagSearch: (message) => this.setLabel(turn, { kind: "rag_search", message }),
      onModerationChecking: () => this.setLabel(turn, { kind: "moderation" }),
      onDelta: (text) => {
        buf.push(text);
        this.scheduleTextFlush(turn);
      },
      onSnapshot: (text) => {
        buf.replace(text);
        this.flushText(run, true);
      },
      onSnapshotThink: (text) => {
        if (!text) return;
        const entries = this.liveThinking.get()[run] ?? [];
        const key = entries[0]?.id ?? "snapshot";
        this.setThinking(run, applyThinkText(entries, key, text, "replace"));
      },
      onUsage: (u) => {
        const prev = turn.assistant.usage;
        const take = (next: number | undefined, old: number) => (typeof next === "number" && next > 0 ? next : old);
        this.updateAssistant(turn, {
          usage: {
            input: take(u.input_tokens, prev.input),
            output: take(u.output_tokens, prev.output),
            cacheRead: take(u.cache_read_tokens, prev.cacheRead),
            cacheWrite: take(u.cache_write_tokens, prev.cacheWrite),
            reasoning: take(u.reasoning_tokens, prev.reasoning),
          },
        });
      },
      onMediaStatus: (e) => {
        const type = e.content_type === "video" ? "video" : e.content_type === "image" ? "image" : undefined;
        this.updateAssistant(turn, (m) => ({
          contentType: type ?? m.contentType,
          live: m.live ? { ...m.live, label: { kind: "media", message: e.message, status: e.status } } : m.live,
        }));
      },
      onMediaImage: (e) => {
        const mime = e.mime_type || "image/png";
        this.updateAssistant(turn, (m) => ({
          contentType: "image",
          live: m.live ? { ...m.live, preview: `data:${mime};base64,${e.b64_json}` } : m.live,
        }));
      },
      onCompact: (e) =>
        this.updateAssistant(turn, { compaction: { method: e.method, freedTokens: e.freed_tokens, keptTurns: e.kept_turns, summaryPreview: e.summary_preview } }),
      onProcess: (e) => {
        const trace = this.traceFromEvent(turn.assistant.trace, e);
        if (trace) this.updateAssistant(turn, { trace });
      },
      onThink: (e) => this.applyThink(turn, e),
    };
  }

  private traceFromEvent(prev: ChatMessage["trace"], e: { block?: unknown; trace?: unknown }): ProcessTrace | null {
    if (e.trace && typeof e.trace === "object") return mergeTrace(prev, e.trace as ProcessTrace);
    if (e.block && typeof e.block === "object") {
      return mergeTrace(prev, { ...(prev ?? { enabled: true }), process: e.block as ProcessTrace["process"] });
    }
    return null;
  }

  // --- buffers ---------------------------------------------------------------------

  private textBufferOf(run: string): TextBuffer {
    let b = this.textBuffers.get(run);
    if (!b) this.textBuffers.set(run, (b = new TextBuffer()));
    return b;
  }

  private scheduleTextFlush(turn: Turn): void {
    if (this.textTimers.has(turn.runId)) return;
    this.textTimers.set(
      turn.runId,
      setTimeout(() => {
        this.textTimers.delete(turn.runId);
        this.flushText(turn.runId, false);
      }, TEXT_FLUSH_MS),
    );
  }

  /** Moves buffered text onto the reply. */
  private flushText(run: string, now: boolean): void {
    const turn = this.active.get(run);
    const buf = this.textBuffers.get(run);
    if (now) {
      const timer = this.textTimers.get(run);
      if (timer) clearTimeout(timer);
      this.textTimers.delete(run);
    }
    if (!turn || !buf || !buf.hasPending) return;
    const { replace, append } = buf.take();
    const base = replace ?? turn.assistant.content;
    const content = base + append;
    const live = turn.assistant.live;
    const cleared = content.length > 0 && live?.label ? { ...live, label: null } : live;
    this.updateAssistant(turn, { content, live: cleared });
  }

  private setThinking(run: string, entries: LiveThinkEntry[]): void {
    this.liveThinking.set((s) => ({ ...s, [run]: entries }));
  }

  private thinkClear(run: string): void {
    this.thinkBuffers.get(run)?.clear();
    this.liveThinking.set((s) => {
      if (!(run in s)) return s;
      const { [run]: _gone, ...rest } = s;
      return rest;
    });
  }

  private applyThink(turn: Turn, e: Parameters<NonNullable<RunStreamHandlers["onThink"]>>[0]): void {
    const run = turn.runId;
    const key = thinkKey(e);
    const meta = { title: e.title, summary: e.summary, status: e.status, startedAt: e.startedAt, endedAt: e.endedAt, roundId: e.roundID };
    let entries = this.liveThinking.get()[run] ?? [];
    const buf = this.thinkBufferOf(run);

    if (e.trace && typeof e.trace === "object") {
      this.updateAssistant(turn, { trace: mergeTrace(turn.assistant.trace, e.trace as ProcessTrace) });
    }
    if (typeof e.contentMarkdown === "string" && e.contentMarkdown !== "") {
      for (const c of buf.flushKey(key)) entries = applyThinkText(entries, c.key, c.text, "append");
      entries = applyThinkText(entries, key, e.contentMarkdown, "replace", meta);
    } else if (typeof e.delta === "string" && e.delta !== "") {
      for (const c of buf.push(key, e.delta)) entries = applyThinkText(entries, c.key, c.text, "append");
      entries = applyThinkText(entries, key, "", "append", meta);
      this.scheduleThinkTick(turn);
    } else {
      entries = applyThinkText(entries, key, "", "append", meta);
    }
    if (terminalThinkStatus(e.status)) {
      for (const c of buf.flushKey(key)) entries = applyThinkText(entries, c.key, c.text, "append");
    }
    this.setThinking(run, entries);
    this.bump(this.stateOf(turn));
  }

  private thinkBufferOf(run: string): ThinkBuffer {
    let b = this.thinkBuffers.get(run);
    if (!b) this.thinkBuffers.set(run, (b = new ThinkBuffer()));
    return b;
  }

  private scheduleThinkTick(turn: Turn): void {
    const run = turn.runId;
    if (this.thinkTimers.has(run)) return;
    this.thinkTimers.set(
      run,
      setTimeout(() => {
        this.thinkTimers.delete(run);
        const buf = this.thinkBuffers.get(run);
        const chunk = buf?.tick();
        if (chunk) {
          this.setThinking(run, applyThinkText(this.liveThinking.get()[run] ?? [], chunk.key, chunk.text, "append"));
          this.bump(this.stateOf(turn));
        }
        if (buf?.hasPending) this.scheduleThinkTick(turn);
      }, THINK_FLUSH_MS),
    );
  }

  /** End of stream: all waiting thinking text goes out at once. */
  private thinkFlush(turn: Turn): void {
    const run = turn.runId;
    const timer = this.thinkTimers.get(run);
    if (timer) clearTimeout(timer);
    this.thinkTimers.delete(run);
    const buf = this.thinkBuffers.get(run);
    if (!buf) return;
    let entries = this.liveThinking.get()[run] ?? [];
    for (const c of buf.flushAll()) entries = applyThinkText(entries, c.key, c.text, "append");
    if (entries.length > 0) this.setThinking(run, entries);
  }

  // --- creating a conversation for a draft ------------------------------------------

  private rekey(turn: Turn, created: ConversationDTO): void {
    const from = turn.key;
    const st = this.states.get(from);
    if (st && from === DRAFT_KEY) {
      this.states.delete(from);
      st.key = created.publicID;
      for (const t of st.turns) {
        t.key = created.publicID;
        t.conversationId = created.publicID;
      }
      this.states.set(created.publicID, st);
      this.views.delete(from);
      if (this.visibleKey === from) this.visibleKey = created.publicID;
      this.states.set(DRAFT_KEY, emptyState(DRAFT_KEY));
      // Queued messages of the draft follow the conversation.
      this.queueStore.set((q) => q.map((item) => (item.conversationKey === DRAFT_KEY ? { ...item, conversationKey: created.publicID } : item)));
      for (const [id, params] of this.queuedParams) if (params.key === DRAFT_KEY) this.queuedParams.set(id, { ...params, key: created.publicID });
      for (const l of [...(this.listeners.get(from) ?? [])]) l();
      this.bump(st);
    }
    this.deps.runs.rebind(turn.runId, created.publicID);
    this.emit("runStarted", { runId: turn.runId, conversationId: created.publicID });
    this.emit("conversationCreated", { fromKey: from, conversation: created });
  }

  private failCreate(turn: Turn, p: SendParams, error: unknown): void {
    const st = this.stateOf(turn);
    st.turns = st.turns.filter((t) => t !== turn);
    turn.stage = "failed";
    this.restoreComposer(turn, p);
    this.releaseTurn(turn);
    this.bump(st);
    this.notice("createFailed", { error });
  }

  private applyOptimisticTitle(convId: string, content: string, turn: Turn): void {
    const conv = this.deps.conversations.find(convId);
    if (conv && !isPlaceholderTitle(conv.title)) return;
    const title = makeOptimisticTitle(content);
    if (!title) return;
    turn.optimisticTitle = title;
    this.deps.conversations.patch(convId, { title });
    void this.deps.conversations.rename(convId, title).catch(() => undefined);
  }

  // --- outcomes --------------------------------------------------------------------

  private restoreComposer(turn: Turn, p: SendParams): void {
    if (!turn.fromComposer || !p.restoreOnFailure || !p.restore) return;
    if (p.restore()) turn.restored = true;
  }

  private settle(turn: Turn, data: SendResultDTO, error: { requestId?: string; message: string; debug?: unknown } | null, p: SendParams): void {
    this.settleData(turn, data, error);
    void this.afterTerminal(turn, p);
  }

  private settleData(turn: Turn, data: SendResultDTO, error: { requestId?: string; message: string; debug?: unknown } | null): void {
    const st = this.stateOf(turn);
    turn.hint = data.metadataRefreshHint;
    const idMap: Record<string, string> = {};
    let user: ChatMessage | null = null;
    if (data.userMessage) {
      const mapped = mapMessage(data.userMessage);
      const previews = new Map(turn.attachments.filter((a) => a.previewUrl).map((a) => [a.fileId, a.previewUrl] as const));
      user = { ...mapped, attachments: mapped.attachments.map((a) => (previews.has(a.fileId) ? { ...a, previewUrl: previews.get(a.fileId) } : a)) };
      if (turn.user) idMap[turn.user.id] = user.id;
    }
    let assistant: ChatMessage | null = null;
    if (data.assistantMessage) {
      const mapped = mapMessage(data.assistantMessage);
      const live = turn.assistant.usage;
      const userUsage = user?.usage ?? turn.user?.usage ?? EMPTY_USAGE;
      const inherit = (own: number, u: number, l: number) => (own > 0 ? own : u > 0 ? u : l);
      assistant = {
        ...mapped,
        usage: turn.assistantOnly
          ? mapped.usage
          : {
              ...mapped.usage,
              input: inherit(mapped.usage.input, userUsage.input, live.input),
              cacheRead: inherit(mapped.usage.cacheRead, userUsage.cacheRead, live.cacheRead),
              cacheWrite: inherit(mapped.usage.cacheWrite, userUsage.cacheWrite, live.cacheWrite),
            },
        compaction: turn.assistant.compaction,
      };
      if (assistant.status === "blocked") assistant = { ...assistant, content: "", attachments: [], trace: null };
      if (error && assistant.alert && !assistant.alert.errorId && error.requestId) {
        assistant = { ...assistant, alert: { ...assistant.alert, errorId: assistant.errorRequestId || error.requestId } };
      }
      idMap[turn.assistant.id] = assistant.id;
    }
    st.selection = rewriteSelection(st.selection, idMap);
    turn.result = { user, assistant };
    turn.stage = "settled";
    turn.terminal = true;
    this.bump(st);
  }

  private settleBlocked(turn: Turn, event: { eventID?: string; categories?: string[]; billedReason?: string; data?: SendResultDTO }, p: SendParams): void {
    if (event.data) {
      this.settleData(turn, event.data, null);
    } else {
      const alert: MessageAlert = {
        kind: "blocked",
        title: "blocked",
        lines: [
          { key: "description" },
          ...(event.eventID ? [{ key: "eventId" as const, value: event.eventID }] : []),
          ...(event.categories?.length ? [{ key: "categories" as const, value: event.categories.join(", ") }] : []),
          ...(event.billedReason === "moderation_blocked_upstream_usage" ? [{ key: "billed" as const }] : []),
        ],
      };
      this.updateAssistant(turn, { status: "blocked", content: "", attachments: [], trace: null, alert, live: undefined });
      turn.stage = "failed";
    }
    this.notice("moderation");
    void this.afterTerminal(turn, p, { blocked: true });
  }

  private finishAborted(turn: Turn): void {
    // Live thinking so far stays with the reply's trace.
    const entries = this.liveThinking.get()[turn.runId] ?? [];
    const text = entries.map((e) => e.text).filter(Boolean).join("\n\n");
    const trace = turn.assistant.trace;
    this.updateAssistant(turn, {
      status: "canceled",
      live: undefined,
      trace: text && !trace ? { enabled: true, upstreamThink: { contentMarkdown: text } } : trace,
    });
    turn.stage = "aborted";
    for (const a of turn.attachments) if (a.previewUrl && !turn.restored) this.deps.revokeUrl(a.previewUrl);
    const key = turn.key;
    if (this.visibleKey === key && turn.conversationId) void this.load(key, { reload: true });
  }

  private failSend(turn: Turn, p: SendParams, error: unknown): void {
    const { reason, status, requestId, debug } = summaryFor(error, "");
    const summary = upstreamSummary({ message: reason, debug, fallback: reason });
    const isHttpSummary = status > 0 && !/^HTTP \d{3}/.test(summary);
    const alert: MessageAlert = {
      kind: "interrupted",
      title: "interrupted",
      lines: [],
      message: reason,
      errorCode: error instanceof ApiError ? error.errorCode : undefined,
      errorId: requestId || undefined,
      debug,
    };
    this.updateAssistant(turn, {
      status: "error",
      live: undefined,
      alert,
      errorMessage: reason,
      errorCode: error instanceof ApiError ? error.errorCode ?? "" : "",
      errorRequestId: requestId,
    });
    turn.stage = "failed";
    this.restoreComposer(turn, p);
    this.notice("sendFailed", { error, summary: isHttpSummary ? `HTTP ${status}: ${summary}` : summary });
    if (turn.conversationId) {
      const id = turn.conversationId;
      void this.deps.conversations.fetch(id).then((c) => this.deps.conversations.patch(id, c), () => undefined);
      if (this.visibleKey === turn.key) void this.load(turn.key, { reload: true });
    }
    for (const a of turn.attachments) if (a.previewUrl && !turn.restored) this.deps.revokeUrl(a.previewUrl);
  }

  /** Bookkeeping after a terminal result: record patches, reload, polling, notification. */
  private async afterTerminal(turn: Turn, p: SendParams, opts: { blocked?: boolean } = {}): Promise<void> {
    const convId = turn.conversationId;
    if (!convId) return;
    const assistant = turn.result?.assistant;
    const ok = assistant?.status === "success";
    // Patch the local record.
    const conv = this.deps.conversations.find(convId);
    const count = Math.max(conv?.messageCount ?? 0, this.stateOf(turn).server.length) + (turn.assistantOnly ? 1 : 2);
    const patch: Partial<ConversationDTO> = { updatedAt: new Date(this.deps.now()).toISOString(), messageCount: count };
    const latest = this.lastModelRun.get(convId) ?? 0;
    if (turn.startedAt >= latest) {
      this.lastModelRun.set(convId, turn.startedAt);
      patch.model = turn.model;
    }
    this.deps.conversations.patch(convId, patch);

    if (this.visibleKey === turn.key) {
      await this.load(turn.key, { reload: true });
    } else {
      this.dropTurn(turn);
    }
    if (ok && assistant) {
      this.emit("completed", { conversationId: convId, text: assistant.content, title: conv?.title ?? "" });
    }
    for (const a of turn.attachments) if (a.previewUrl && !turn.restored && !this.states.get(turn.key)?.turns.includes(turn)) this.deps.revokeUrl(a.previewUrl);
    if (!opts.blocked && (ok || turn.hint === "pending")) {
      this.pollMetadata(convId, turn.optimisticTitle, turn.hint, p.autoGenerateLabels);
    }
    this.pump();
  }

  private dropTurn(turn: Turn): void {
    const st = this.states.get(turn.key);
    if (!st) return;
    if (!st.turns.includes(turn)) return;
    st.turns = st.turns.filter((t) => t !== turn);
    this.releaseTurn(turn);
    this.bump(st);
  }

  // --- metadata polling ---------------------------------------------------------------

  private pollMetadata(convId: string, optimistic: string, hint: string | undefined, autoLabels: boolean): void {
    if (this.metaPolls.has(convId)) return;
    const conv = this.deps.conversations.find(convId);
    const labels = parseLabelList(conv?.labelsJSON);
    if (!shouldPoll({ title: conv?.title ?? "", optimisticTitle: optimistic, labels, autoGenerateLabels: autoLabels, hint })) return;
    this.metaPolls.add(convId);
    const started = this.deps.now();
    let delay: number | null = null;
    const step = () => {
      delay = nextDelay(delay);
      if (this.deps.now() - started + delay > GIVE_UP_MS) {
        this.metaPolls.delete(convId);
        return;
      }
      setTimeout(async () => {
        try {
          const fresh = await this.deps.conversations.fetch(convId);
          const known = this.deps.conversations.find(convId);
          const prevLabels = parseLabelList(known?.labelsJSON);
          const nextLabels = parseLabelList(fresh.labelsJSON);
          if (metadataChanged({ title: known?.title ?? "", labels: prevLabels }, { title: fresh.title, labels: nextLabels }, optimistic)) {
            this.deps.conversations.patch(convId, { title: fresh.title, labelsJSON: fresh.labelsJSON });
          }
          const now = this.deps.conversations.find(convId);
          if (!shouldPoll({ title: now?.title ?? fresh.title, optimisticTitle: optimistic, labels: parseLabelList(now?.labelsJSON ?? fresh.labelsJSON), autoGenerateLabels: autoLabels, hint: undefined })) {
            this.metaPolls.delete(convId);
            return;
          }
        } catch {
          /* retried on the schedule */
        }
        step();
      }, delay);
    };
    step();
  }

  // --- stop ---------------------------------------------------------------------------

  /** Stops the reply at the end of the visible branch. */
  async stop(key: string): Promise<void> {
    const v = this.view(key);
    const last = v.path[v.path.length - 1];
    if (!last || last.role !== "assistant") return;
    await this.stopRun(key, last.runId, last.status === "pending");
  }

  async stopRun(key: string, runId: string, pending = true): Promise<void> {
    const turn = this.active.get(runId);
    const st = this.states.get(key);
    if (!turn) {
      if (st?.attach?.runId === runId) {
        this.detach(st);
        try {
          const res = await this.deps.messages.cancelRun(runId);
          if (res.canceled) {
            this.deps.runs.noticeFinished(runId, false);
            this.emit("runFinished", { runId, conversationId: key });
          }
        } catch {
          /* the reload shows the truth */
        }
        await this.load(key, { reload: true });
        return;
      }
      if (pending && runId) {
        try {
          await this.deps.messages.cancelRun(runId);
        } catch {
          /* ignored */
        }
        await this.load(key, { reload: true });
      }
      return;
    }
    if (!this.deps.hasToken()) {
      turn.controller.abort();
      return;
    }
    if (turn.cancelRequested) return;
    turn.cancelRequested = true;
    turn.cancelTimer = setTimeout(() => this.abortAndReload(turn), CANCEL_GRACE_MS);
    try {
      await this.deps.messages.cancelRun(runId);
    } catch {
      this.abortAndReload(turn);
    }
  }

  private abortAndReload(turn: Turn): void {
    this.clearCancelTimer(turn);
    if (!this.active.has(turn.runId)) return;
    turn.controller.abort();
    if (this.visibleKey === turn.key) void this.load(turn.key, { reload: true });
  }

  private clearCancelTimer(turn: Turn): void {
    if (turn.cancelTimer) clearTimeout(turn.cancelTimer);
    turn.cancelTimer = null;
  }

  // --- re-attaching to runs that continued on the server --------------------------------

  private clearPendingTimer(key: string): void {
    const t = this.pendingTimers.get(key);
    if (t) clearTimeout(t);
    this.pendingTimers.delete(key);
  }

  private detach(st: ConvState): void {
    if (!st.attach) return;
    st.attach.controller.abort();
    this.cursors.delete(st.attach.runId);
    this.cursorText.delete(st.attach.runId);
    st.attach = null;
    this.bump(st);
  }

  /** Looks for a pending reply of the visible conversation and follows it. */
  private syncPending(key: string): void {
    if (this.visibleKey !== key || key === DRAFT_KEY) return;
    const st = this.states.get(key);
    if (!st || !st.loadedOnce) return;
    let pending: ChatMessage | undefined;
    for (let i = st.server.length - 1; i >= 0; i--) {
      const m = st.server[i] as ChatMessage;
      if (m.role === "assistant" && m.status === "pending") {
        pending = m;
        break;
      }
    }
    if (!pending) {
      this.clearPendingTimer(key);
      return;
    }
    if (pending.runId && this.active.has(pending.runId)) return;
    if (pending.runId && st.attach?.runId === pending.runId) return;
    const recentlyEnded = pending.runId && this.deps.now() - (this.lastAttachEnd.get(pending.runId) ?? -Infinity) < RESUME_POLL_MS;
    if (pending.runId && !recentlyEnded) {
      void this.attach(st, pending);
      return;
    }
    this.clearPendingTimer(key);
    this.pendingTimers.set(
      key,
      setTimeout(() => {
        this.pendingTimers.delete(key);
        void this.load(key, { reload: true });
      }, RESUME_POLL_MS),
    );
  }

  private async attach(st: ConvState, pending: ChatMessage): Promise<void> {
    if (st.attach) this.detach(st);
    const runId = pending.runId;
    const controller = new AbortController();
    const attach: AttachState = {
      runId, controller, cursor: this.cursors.get(runId) ?? 0, text: null,
      usage: { input: 0, output: 0, cacheRead: 0, cacheWrite: 0, reasoning: 0 }, trace: null, label: null, contentType: null, blocked: false,
    };
    st.attach = attach;
    this.bump(st);
    const base = pending.content;
    // A replay after the snapshot repeats text the snapshot has; see ReplayDedupe.
    const requestedAfter = attach.cursor;
    const atCursor = requestedAfter === 0 ? "" : (this.cursorText.get(runId) ?? null);
    const dedupe = new ReplayDedupe();
    // The snapshot's thinking text is shown in its own entry; once the replay has
    // supplied that much thinking itself, the snapshot entry would only repeat it.
    let replayWorld = false;
    let snapshotThinkLen = 0;
    let replayedThinkLen = (this.liveThinking.get()[runId] ?? []).reduce((n, e) => n + e.text.length, 0);
    const patch = (fn: (a: AttachState) => void) => {
      if (st.attach !== attach) return;
      fn(attach);
      this.bump(st);
    };
    const current = () => attach.text ?? base;
    try {
      const outcome = await this.deps.streams.resume(runId, attach.cursor, controller.signal, {
        onSeq: (seq) => {
          attach.cursor = seq;
          this.cursors.set(runId, seq);
        },
        onSnapshot: (text, meta) => {
          // The first snapshot opens the replay window; a later one is a replace event inside the replay.
          if (attach.text === null) {
            replayWorld = (meta?.seq ?? 0) === requestedAfter;
            dedupe.begin(text, meta?.seq ?? 0, requestedAfter, atCursor);
          } else dedupe.stop();
          patch((a) => (a.text = text));
        },
        onDelta: (text) => {
          const add = dedupe.push(text);
          if (add) patch((a) => (a.text = current() + add));
        },
        onSnapshotThink: (text) => {
          if (!text) return;
          if (replayWorld) snapshotThinkLen = text.length;
          this.setThinking(runId, applyThinkText(this.liveThinking.get()[runId] ?? [], "snapshot", text, "replace"));
        },
        onThink: (e) => {
          const key = thinkKey(e);
          let entries = this.liveThinking.get()[runId] ?? [];
          const meta = { title: e.title, summary: e.summary, status: e.status, startedAt: e.startedAt, endedAt: e.endedAt, roundId: e.roundID };
          if (typeof e.contentMarkdown === "string" && e.contentMarkdown) entries = applyThinkText(entries, key, e.contentMarkdown, "replace", meta);
          else entries = applyThinkText(entries, key, e.delta ?? "", "append", meta);
          replayedThinkLen += e.delta?.length ?? 0;
          if (snapshotThinkLen > 0 && replayedThinkLen >= snapshotThinkLen) {
            entries = entries.filter((x) => x.id !== "snapshot");
            snapshotThinkLen = 0;
          }
          this.setThinking(runId, entries);
          this.bump(st);
        },
        onProcess: (e) => {
          const trace = this.traceFromEvent(attach.trace ?? pending.trace, e);
          if (trace) patch((a) => (a.trace = trace));
        },
        onUsage: (u) =>
          patch((a) => {
            const take = (n: number | undefined, old: number) => (typeof n === "number" && n > 0 ? n : old);
            a.usage = {
              input: take(u.input_tokens, a.usage.input),
              output: take(u.output_tokens, a.usage.output),
              cacheRead: take(u.cache_read_tokens, a.usage.cacheRead),
              cacheWrite: take(u.cache_write_tokens, a.usage.cacheWrite),
              reasoning: take(u.reasoning_tokens, a.usage.reasoning),
            };
          }),
        onFileProc: (message) => patch((a) => (a.label = { kind: "file_proc", message })),
        onRagSearch: (message) => patch((a) => (a.label = { kind: "rag_search", message })),
        onModerationChecking: () => patch((a) => (a.label = { kind: "moderation" })),
        onMediaStatus: (e) =>
          patch((a) => {
            a.label = { kind: "media", message: e.message, status: e.status };
            if (e.content_type === "image" || e.content_type === "video") a.contentType = e.content_type;
          }),
        onMediaImage: (e) =>
          patch((a) => {
            a.contentType = "image";
            a.preview = `data:${e.mime_type || "image/png"};base64,${e.b64_json}`;
          }),
      });
      if (outcome.kind === "blocked") patch((a) => (a.blocked = true));
    } catch (error) {
      if (isAbortError(error)) return;
    }
    if (st.attach === attach) {
      st.attach = null;
      this.cursors.delete(runId);
      this.cursorText.delete(runId);
      this.lastAttachEnd.set(runId, this.deps.now());
      this.bump(st);
      await this.load(st.key, { reload: true });
      this.pump();
    }
  }

  // --- queue -------------------------------------------------------------------------

  /** True when a send from this conversation should queue instead of starting. */
  shouldQueue(key: string): boolean {
    if (this.branchHasActiveStream(key)) return true;
    const v = this.view(key);
    return v.busy;
  }

  enqueue(p: SendParams): QueuedMessage | null {
    const text = p.text.trim();
    if (!text && p.attachments.length === 0) return null;
    const v = this.view(p.key);
    const last = v.path[v.path.length - 1];
    const activeRun = last && last.role === "assistant" && last.status === "pending" ? last.runId || null : this.latestRunningRun(p.key);
    if (!activeRun && !last) return null;
    const queue = this.queueStore.get();
    const item: QueuedMessage = {
      id: `q_${newRunId().slice(4, 16)}`,
      runId: newRunId(),
      conversationKey: p.key,
      text,
      attachments: p.attachments,
      model: p.model,
      options: p.options,
      selections: p.selections,
      parentRunId: parentRunForNew(queue, p.key, activeRun),
      parentMessageId: null,
      createdAt: this.deps.now(),
    };
    this.queuedParams.set(item.id, { ...p, runId: item.runId, fromComposer: false, restore: undefined });
    this.queueStore.set(enqueue(queue, item));
    this.ensureStatusPolling();
    this.pump();
    return item;
  }

  private latestRunningRun(key: string): string | null {
    const st = this.states.get(key);
    const t = st?.turns.filter((x) => x.stage === "running" && x.branchReason === "default").pop();
    return t?.runId ?? st?.attach?.runId ?? null;
  }

  removeQueued(id: string): void {
    const res = removeItem(this.queueStore.get(), id);
    if (res.removed) this.discardQueued(res.removed);
    this.queueStore.set(res.queue);
    this.pump();
  }

  editQueued(id: string, text: string): void {
    const res = editItem(this.queueStore.get(), id, text);
    if (res.removed) this.discardQueued(res.removed);
    this.queueStore.set(res.queue);
    const params = this.queuedParams.get(id);
    if (params && !res.removed) this.queuedParams.set(id, { ...params, text: text.trim() });
    this.pump();
  }

  /** Sends this queued message next, stopping the running reply. */
  guideQueued(id: string): void {
    const queue = this.queueStore.get();
    const item = queue.find((q) => q.id === id);
    if (!item) return;
    const active = this.latestRunningRun(item.conversationKey);
    this.queueStore.set(guideToFront(queue, id, active));
    const st = this.states.get(item.conversationKey);
    const v = this.view(item.conversationKey);
    const last = v.path[v.path.length - 1];
    if (last && last.role === "assistant" && last.status === "pending" && st) void this.stopRun(item.conversationKey, last.runId, true);
    this.pump();
  }

  private discardQueued(item: QueuedMessage): void {
    for (const a of item.attachments) if (a.previewUrl) this.deps.revokeUrl(a.previewUrl);
    this.queuedParams.delete(item.id);
  }

  clearQueue(key: string): void {
    const queue = this.queueStore.get();
    for (const q of queue) if (q.conversationKey === key) this.discardQueued(q);
    this.queueStore.set(queue.filter((q) => q.conversationKey !== key));
  }

  private outcomeOf(runId: string): ParentOutcome {
    // 1. a turn of this browser
    for (const st of this.states.values()) {
      const t = st.turns.find((x) => x.runId === runId);
      if (!t) continue;
      if (t.stage === "running") return { state: "running" };
      if (t.stage === "failed" || t.stage === "aborted") return { state: "failed" };
      const a = t.result?.assistant;
      if (a && a.persisted && SUCCESS_STATUSES.has(a.status)) return { state: "success", messageId: a.id };
      if (a && FAILED_STATUSES.has(a.status)) return { state: "failed" };
    }
    // 2. server data
    for (const st of this.states.values()) {
      const m = st.server.find((x) => x.role === "assistant" && x.runId === runId);
      if (!m) continue;
      if (m.status === "pending") return { state: "running" };
      if (SUCCESS_STATUSES.has(m.status)) return { state: "success", messageId: m.id };
      if (FAILED_STATUSES.has(m.status)) return { state: "failed" };
    }
    // 3. the run status feed
    const status = this.statusBook.get(runId, this.deps.now());
    if (status) {
      if (FAILED_STATUSES.has(status)) return { state: "failed" };
      if (SUCCESS_STATUSES.has(status)) {
        // The message itself is not loaded yet: fetch and decide on the next pass.
        for (const q of this.queueStore.get()) if (q.parentRunId === runId && this.visibleKey === q.conversationKey) void this.load(q.conversationKey, { reload: true });
        return { state: "running" };
      }
      return { state: "running" };
    }
    this.statusBook.ask(runId, this.deps.now());
    return { state: "unknown" };
  }

  private pump(): void {
    if (this.pumping) return;
    this.pumping = true;
    try {
      for (let guard = 0; guard < 50; guard++) {
        const queue = this.queueStore.get();
        if (queue.length === 0) return;
        const failed = failedChains(queue, (id) => this.outcomeOf(id));
        if (failed.cancelled.length > 0) {
          this.queueStore.set(failed.queue);
          for (const c of failed.cancelled) {
            this.discardQueued(c);
            this.notice("queuedCanceled");
          }
          continue;
        }
        const pick = pickDispatchable(queue, {
          activeStreams: this.active.size,
          cap: LIMITS.concurrentRuns,
          branchBusy: (key) => this.branchHasActiveStream(key) || (this.visibleKey === key && this.view(key).busy),
          unresolvedExchange: (key) => this.states.get(key)?.turns.some((t) => t.stage === "running" && t.branchReason === "default") ?? false,
          parentOutcome: (id) => this.outcomeOf(id),
        });
        if (!pick) return;
        const params = this.queuedParams.get(pick.item.id);
        this.queueStore.set(queue.filter((q) => q.id !== pick.item.id));
        this.queuedParams.delete(pick.item.id);
        if (!params) continue;
        const parentId = pick.parentMessageId ?? lastPersistedLeaf(this.view(pick.item.conversationKey).path)?.id ?? null;
        void this.send({
          ...params,
          key: pick.item.conversationKey,
          text: pick.item.text,
          attachments: pick.item.attachments,
          parentId,
          sourceId: null,
          branchReason: "default",
          runId: pick.item.runId,
          fromComposer: false,
        });
      }
    } finally {
      this.pumping = false;
      this.ensureStatusPolling();
    }
  }

  private ensureStatusPolling(): void {
    if (this.statusTimer) return;
    const waiting = this.queueStore.get().filter((q) => q.parentRunId && this.outcomeUnknownRemote(q.parentRunId));
    if (waiting.length === 0) return;
    this.statusTimer = setTimeout(async () => {
      this.statusTimer = null;
      if (!this.deps.isDocumentVisible()) {
        this.ensureStatusPolling();
        return;
      }
      const ids = [...new Set(this.queueStore.get().map((q) => q.parentRunId).filter((x): x is string => !!x && this.outcomeUnknownRemote(x)))];
      if (ids.length === 0) return;
      try {
        for (let i = 0; i < ids.length; i += 100) {
          const chunk = ids.slice(i, i + 100);
          const answers = await this.deps.messages.runStatuses(chunk);
          this.statusBook.apply(chunk, answers, this.deps.now());
        }
        this.statusFailures = 0;
      } catch {
        this.statusFailures++;
      }
      this.pump();
      this.statusTimer = setTimeout(() => {
        this.statusTimer = null;
        this.ensureStatusPolling();
      }, pollDelay(this.statusFailures));
    }, 0);
  }

  private outcomeUnknownRemote(runId: string): boolean {
    for (const st of this.states.values()) {
      if (st.turns.some((t) => t.runId === runId)) return false;
      if (st.server.some((m) => m.runId === runId && m.role === "assistant" && m.status !== "pending")) return false;
    }
    const status = this.statusBook.get(runId, this.deps.now());
    return !(status && (SUCCESS_STATUSES.has(status) || FAILED_STATUSES.has(status)));
  }

  /** Called when the page becomes visible again. */
  onVisible(): void {
    this.pump();
    if (this.visibleKey) this.syncPending(this.visibleKey);
  }

  dispose(): void {
    for (const t of this.pendingTimers.values()) clearTimeout(t);
    for (const t of this.textTimers.values()) clearTimeout(t);
    for (const t of this.thinkTimers.values()) clearTimeout(t);
    if (this.statusTimer) clearTimeout(this.statusTimer);
    for (const turn of this.active.values()) turn.controller.abort();
  }
}
