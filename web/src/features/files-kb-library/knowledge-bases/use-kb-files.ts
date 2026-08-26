"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "@/components/ui";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { uniqueBy } from "../shared/async";
import { isProcessing, markQueued, MissingTracker, mergeStatuses } from "../shared/file-status";
import { announceKbChanged } from "../shared/invalidation";
import { hasSession } from "../shared/session";
import { useLatest } from "../shared/use-latest";
import type { KnowledgeBase, KnowledgeBaseFile } from "../shared/types";
import { usePolling } from "../shared/use-polling";
import { planVectorize, summarizeEmbedding } from "../shared/vectorize";
import { fetchKbSnapshot, listKbFiles, removeFileFromKb, submitKbEmbeddings, type KbMode } from "./api";
import { FILES_PAGE_SIZE, MAX_FILES_PER_CALL, summaryChanged, toggleCapped, vectorizableIDs } from "./model";

const POLL_INTERVAL = 2500;

export interface KbFilesState {
  /** The base these files belong to; a mismatch with the selected base means "not loaded yet". */
  owner: string | null;
  files: KnowledgeBaseFile[];
  total: number;
  page: number;
  loading: boolean;
  loadingMore: boolean;
  checked: Set<string>;
  /** The first page of this base failed to load. */
  failed: boolean;
}

const EMPTY: KbFilesState = { owner: null, files: [], total: 0, page: 1, loading: true, loadingMore: false, checked: new Set(), failed: false };

export interface UseKbFilesInput {
  mode: KbMode;
  kb: KnowledgeBase | null;
  editable: boolean;
  /** Called when the base's summary changed on the server (poll) or after a local mutation. */
  onSummary: (kb: KnowledgeBase) => void;
  /** Called after a mutation so the list silently reloads. */
  onMutated: (id: string) => void;
}

/** Files of the selected knowledge base, with polling, removal and indexing. */
export function useKbFiles({ mode, kb, editable, onSummary, onMutated }: UseKbFilesInput) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const kbId = kb?.publicID ?? null;
  const [raw, setState] = useState<KbFilesState>(EMPTY);
  // Right after the selection changes, show an empty loading list instead of the previous base's files.
  const state: KbFilesState = raw.owner === kbId ? raw : { ...EMPTY, loading: kbId !== null };
  const [removingId, setRemovingId] = useState<string | null>(null);
  const [submitting, setSubmitting] = useState<ReadonlySet<string>>(new Set());
  const [vectorizeBusy, setVectorizeBusy] = useState(false);

  const latest = useLatest({ state, kb, mode, editable, t, errorMessage, onSummary, onMutated });
  const controller = useRef<AbortController | null>(null);
  const generation = useRef(0);
  const vectorizing = useRef(false);
  const tracker = useRef(new MissingTracker(POLL_INTERVAL));

  const load = useCallback(async (id: string, page: number) => {
    const run = ++generation.current;
    controller.current?.abort();
    const ctrl = new AbortController();
    controller.current = ctrl;
    const { mode: m, t: tr, errorMessage: em } = latest.current;
    if (page === 1) setState({ ...EMPTY, owner: id, loading: true });
    else setState((s) => ({ ...s, loadingMore: true }));
    try {
      if (!(await hasSession())) throw new Error("errors.auth.unauthorized");
      const res = await listKbFiles(m, id, page, FILES_PAGE_SIZE, ctrl.signal);
      if (run !== generation.current) return;
      setState((s) => ({
        owner: id,
        files: page === 1 ? res.results : uniqueBy([...s.files, ...res.results], (f) => f.fileID),
        total: res.total,
        page,
        loading: false,
        loadingMore: false,
        checked: page === 1 ? new Set() : s.checked,
        failed: false,
      }));
    } catch (error) {
      if (isAbortError(error) || run !== generation.current) return;
      setState((s) => ({ ...s, loading: false, loadingMore: false, failed: page === 1 }));
      toast.error(tr("kbWs.toasts.loadFailed"), { id: "kb-files-load-failed", description: em(error) });
    }
  }, [latest]);

  // Switching bases clears the list at once and loads the new one.
  useEffect(() => {
    tracker.current.reset();
    if (!kbId) {
      generation.current += 1;
      controller.current?.abort();
      setState({ ...EMPTY, loading: false });
      return;
    }
    setState({ ...EMPTY, owner: kbId, loading: true });
    void load(kbId, 1);
    return () => {
      generation.current += 1;
      controller.current?.abort();
    };
  }, [kbId, load]);

  const reload = useCallback(() => {
    if (kbId) void load(kbId, 1);
  }, [kbId, load]);

  const loadMore = useCallback(() => {
    const s = latest.current.state;
    if (!kbId || s.loading || s.loadingMore || s.files.length >= s.total) return;
    void load(kbId, s.page + 1);
  }, [kbId, load, latest]);

  // Polling while the base reports work in progress.
  const anyProcessing = useMemo(() => state.files.some(isProcessing), [state.files]);
  const shouldPoll = kbId !== null && !state.loading && ((kb?.processingFileCount ?? 0) > 0 || anyProcessing);
  usePolling(shouldPoll, POLL_INTERVAL, async (signal) => {
    const cur = latest.current;
    if (!cur.kb) return;
    const ids = cur.state.files.filter(isProcessing).map((f) => f.fileID).slice(0, 100);
    const snap = await fetchKbSnapshot(cur.mode, cur.kb.publicID, ids, signal);
    if (signal.aborted || latest.current.kb?.publicID !== cur.kb.publicID) return;
    const statuses = snap.statuses ?? [];
    const removed = new Set(tracker.current.observe(ids, statuses.map((s) => s.fileID), Date.now()));
    setState((s) => {
      const merged = mergeStatuses(s.files, statuses);
      const files = removed.size > 0 ? merged.files.filter((f) => !removed.has(f.fileID)) : merged.files;
      const total = snap.knowledgeBase ? snap.knowledgeBase.fileCount : s.total;
      if (!merged.changed && files === s.files && total === s.total) return s;
      return { ...s, files, total };
    });
    const fresh = snap.knowledgeBase;
    if (fresh && summaryChanged(cur.kb, fresh)) {
      cur.onSummary(fresh);
      announceKbChanged(fresh.publicID);
    }
  });

  const toggleChecked = useCallback(
    (id: string) => setState((s) => ({ ...s, checked: toggleCapped(s.checked, id, MAX_FILES_PER_CALL) })),
    [],
  );
  const setAllChecked = useCallback(
    (on: boolean) =>
      setState((s) => ({ ...s, checked: on ? new Set(vectorizableIDs(s.files).slice(0, MAX_FILES_PER_CALL)) : new Set() })),
    [],
  );
  const clearChecked = useCallback(() => setState((s) => ({ ...s, checked: new Set() })), []);

  // Checked files must stay vectorizable.
  useEffect(() => {
    setState((s) => {
      if (s.checked.size === 0) return s;
      const ok = new Set(vectorizableIDs(s.files));
      const next = new Set([...s.checked].filter((id) => ok.has(id)));
      return next.size === s.checked.size ? s : { ...s, checked: next };
    });
  }, [state.files]);

  const removeFile = useCallback(
    async (fileID: string) => {
      const cur = latest.current;
      if (!cur.kb || !cur.editable || removingId) return;
      const id = cur.kb.publicID;
      controller.current?.abort();
      generation.current += 1;
      setRemovingId(fileID);
      try {
        await removeFileFromKb(cur.mode, id, fileID);
        setState((s) => {
          const checked = new Set(s.checked);
          checked.delete(fileID);
          return { ...s, files: s.files.filter((f) => f.fileID !== fileID), total: Math.max(0, s.total - 1), checked, loading: false, loadingMore: false };
        });
        cur.onMutated(id);
        announceKbChanged(id);
        toast.success(cur.t("kbWs.toasts.removed"));
      } catch (error) {
        setState((s) => ({ ...s, loading: false, loadingMore: false }));
        toast.error(cur.t("kbWs.toasts.removeFailed"), { description: cur.errorMessage(error) });
      } finally {
        setRemovingId(null);
      }
    },
    [removingId, latest],
  );

  const vectorize = useCallback(async (requested: string[]) => {
    const cur = latest.current;
    if (vectorizing.current || !cur.editable) return;
    const plan = planVectorize(requested);
    if (plan.ids.length === 0) return;
    if (plan.tooMany) {
      toast.error(cur.t("kbWs.toasts.limit", { max: MAX_FILES_PER_CALL }));
      return;
    }
    if (!(await hasSession())) {
      toast.error(cur.t("kbWs.toasts.sessionExpired"), { description: cur.t("kbWs.toasts.signIn") });
      return;
    }
    vectorizing.current = true;
    setVectorizeBusy(true);
    setSubmitting(new Set(plan.ids));
    try {
      const res = await submitKbEmbeddings(cur.mode, plan.ids);
      const done = new Set(res.submittedFileIDs ?? []);
      setState((s) => ({
        ...s,
        files: s.files.map((f) => (done.has(f.fileID) ? markQueued(f) : f)),
        checked: new Set([...s.checked].filter((id) => !done.has(id))),
      }));
      const outcome = summarizeEmbedding(res);
      if (outcome.kind === "partial") toast.warning(cur.t("kbWs.toasts.indexPartial", { submitted: outcome.submitted, failed: outcome.failed }));
      else if (outcome.kind === "nothing") toast.info(cur.t("kbWs.toasts.indexNothing"));
      else
        toast.success(cur.t("kbWs.toasts.indexQueued", { count: outcome.count }), {
          description: outcome.skipped > 0 ? cur.t("kbWs.toasts.indexSkipped", { count: outcome.skipped }) : undefined,
        });
    } catch (error) {
      toast.error(cur.t("kbWs.toasts.indexFailed"), { description: cur.errorMessage(error) });
    } finally {
      vectorizing.current = false;
      setVectorizeBusy(false);
      setSubmitting(new Set());
    }
  }, [latest]);

  return { state, reload, loadMore, toggleChecked, setAllChecked, clearChecked, removeFile, removingId, vectorize, submitting, vectorizeBusy };
}

export type KbFiles = ReturnType<typeof useKbFiles>;
