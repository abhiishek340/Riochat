"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "@/components/ui";
import { isAbortError } from "@/lib/api/errors";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { chunk, uniqueBy } from "../shared/async";
import { isProcessing, MissingTracker, mergeStatuses } from "../shared/file-status";
import { subscribeFilesChanged } from "../shared/invalidation";
import { hasSession } from "../shared/session";
import { useLatest } from "../shared/use-latest";
import type { FileObject, StorageQuota } from "../shared/types";
import { usePolling } from "../shared/use-polling";
import { fetchStatuses, listFiles, locateFile } from "./api";
import { appendPage, PAGE_SIZE, pickSelection, pruneChecked, toggleFilter } from "./list-model";

export type ListPhase = "loading" | "idle" | "syncing" | "more";

export interface LibraryState {
  items: FileObject[];
  total: number;
  page: number;
  hasMore: boolean;
  quota: StorageQuota | null;
  selectedId: string | null;
  checked: Set<string>;
  phase: ListPhase;
  /** The last visible (non-background) first-page load failed. */
  loadFailed: boolean;
  /** The last next-page load failed: automatic paging stays off until the user retries. */
  moreFailed: boolean;
}

const INITIAL: LibraryState = {
  items: [],
  total: 0,
  page: 1,
  hasMore: false,
  quota: null,
  selectedId: null,
  checked: new Set(),
  phase: "loading",
  loadFailed: false,
  moreFailed: false,
};

const POLL_INTERVAL = 2000;
/** A background reload re-reads the pages already on screen, up to this many. */
const MAX_REFRESH_PAGES = 10;

type LoadMode = "replace" | "more" | "background";

interface LoadOptions {
  mode: LoadMode;
  preferred?: string | null;
  /** Fetch `preferred` separately when it is not on the first page (deep links). */
  ensure?: boolean;
}

/**
 * Owns the loaded list: search, filters, sort, paging, selection, status
 * polling and cross-tab invalidation. Mutations live in `useFileActions`.
 */
export function useFileLibrary(deepLinkFile: string | null) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [state, setState] = useState<LibraryState>(INITIAL);
  const [query, setQuery] = useState("");
  const [filters, setFilters] = useState<string[]>([]);
  const [sort, setSort] = useState("created");
  const [uploading, setUploading] = useState(false);
  const debouncedQuery = useDebouncedValue(query, 250);
  const kindsKey = filters.join(",");

  // Latest values for async callbacks.
  const latest = useLatest({ state, debouncedQuery, filters, sort, uploading, t, errorMessage });

  const sequence = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const everLoaded = useRef(false);
  const handledDeepLink = useRef<string | null>(null);
  const tracker = useRef(new MissingTracker(POLL_INTERVAL));

  const load = useCallback(async ({ mode, preferred = null, ensure = false }: LoadOptions) => {
    const run = ++sequence.current;
    controller.current?.abort();
    const ctrl = new AbortController();
    controller.current = ctrl;
    const cur = latest.current;
    // A background reload keeps what the user scrolled through (and the selection inside it).
    const readPages = mode === "background" ? Math.min(Math.max(cur.state.page, 1), MAX_REFRESH_PAGES) : 1;
    const page = mode === "more" ? cur.state.page + 1 : mode === "background" ? readPages : 1;

    if (!(await hasSession())) {
      if (run !== sequence.current) return;
      setState((s) => ({ ...s, items: [], total: 0, hasMore: false, selectedId: null, checked: new Set(), phase: "idle", loadFailed: false }));
      toast.error(cur.t("filesWs.toasts.sessionExpired"), { id: "files-load-failed", description: cur.t("filesWs.toasts.signInToView") });
      return;
    }

    if (mode === "more") setState((s) => ({ ...s, phase: "more", moreFailed: false }));
    else if (mode === "replace") setState((s) => ({ ...s, phase: everLoaded.current ? "syncing" : "loading", loadFailed: false, moreFailed: false }));

    try {
      const readPage = (n: number) => listFiles({ page: n, pageSize: PAGE_SIZE, q: cur.debouncedQuery, kinds: cur.filters, sort: cur.sort }, ctrl.signal);
      const answers = await Promise.all(Array.from({ length: mode === "more" ? 1 : readPages }, (_, i) => readPage(mode === "more" ? page : i + 1)));
      const res = answers[0] as Awaited<ReturnType<typeof readPage>>;
      let results = uniqueBy(answers.flatMap((a) => a.results ?? []), (f) => f.fileID);
      if (mode !== "more" && ensure && preferred && !results.some((f) => f.fileID === preferred)) {
        const found = await locateFile(preferred, PAGE_SIZE, ctrl.signal).catch((e: unknown) => {
          if (isAbortError(e)) throw e;
          return null;
        });
        if (found) results = [found, ...results];
      }
      if (run !== sequence.current) return;
      everLoaded.current = true;
      setState((s) => {
        const items = mode === "more" ? appendPage(s.items, results) : results;
        return {
          items,
          total: res.total ?? items.length,
          page,
          hasMore: page * PAGE_SIZE < (res.total ?? 0),
          quota: res.quota ?? s.quota,
          selectedId: mode === "more" ? s.selectedId : pickSelection(preferred, s.selectedId, items),
          checked: pruneChecked(s.checked, items),
          phase: "idle",
          loadFailed: false,
          moreFailed: false,
        };
      });
    } catch (error) {
      if (isAbortError(error) || run !== sequence.current) return;
      setState((s) => ({
        ...s,
        phase: "idle",
        loadFailed: mode === "replace" ? true : s.loadFailed,
        moreFailed: mode === "more" ? true : s.moreFailed,
      }));
      if (mode !== "background") {
        toast.error(cur.t("filesWs.toasts.loadFailed"), { id: "files-load-failed", description: cur.errorMessage(error) });
      }
    }
  }, [latest]);

  // First page: runs on mount and whenever the search, filters, sort or deep link changes.
  useEffect(() => {
    const fresh = deepLinkFile !== handledDeepLink.current;
    handledDeepLink.current = deepLinkFile;
    void load({ mode: "replace", preferred: fresh ? deepLinkFile : null, ensure: fresh && deepLinkFile !== null });
  }, [debouncedQuery, kindsKey, sort, deepLinkFile, load]);

  useEffect(
    () => () => {
      sequence.current += 1;
      controller.current?.abort();
    },
    [],
  );

  const reload = useCallback((preferred?: string | null) => load({ mode: "background", preferred: preferred ?? null }), [load]);

  /** `force` is the user's own "try again"; automatic paging stops after a failed page. */
  const loadMore = useCallback(
    (force = false) => {
      const cur = latest.current;
      if (cur.state.phase !== "idle" || !cur.state.hasMore || cur.uploading) return;
      if (cur.state.moreFailed && !force) return;
      void load({ mode: "more" });
    },
    [load, latest],
  );

  /** Re-runs the first page with the current search, filters and sort. */
  const retry = useCallback(() => {
    void load({ mode: "replace", preferred: latest.current.state.selectedId });
  }, [load, latest]);

  // Other parts of the app announce deleted conversations or projects.
  useEffect(
    () =>
      subscribeFilesChanged((change) => {
        const quota = change.quota;
        if (quota) setState((s) => ({ ...s, quota: { ...(s.quota ?? {}), ...quota } }));
        void load({ mode: "background", preferred: latest.current.state.selectedId });
      }),
    [load, latest],
  );

  // Status polling for files that are still being worked on.
  const anyProcessing = useMemo(() => state.items.some(isProcessing), [state.items]);
  usePolling(anyProcessing && state.phase === "idle" && !uploading, POLL_INTERVAL, async (signal) => {
    const ids = latest.current.state.items.filter(isProcessing).map((f) => f.fileID);
    if (ids.length === 0) return;
    const answers = (await Promise.all(chunk(ids, 100).map((batch) => fetchStatuses(batch, signal)))).flat();
    if (signal.aborted) return;
    const removed = new Set(tracker.current.observe(ids, answers.map((a) => a.fileID), Date.now()));
    setState((s) => {
      const merged = mergeStatuses(s.items, answers);
      if (!merged.changed && removed.size === 0) return s;
      const items = removed.size > 0 ? merged.files.filter((f) => !removed.has(f.fileID)) : merged.files;
      const gone = s.items.length - items.length;
      return {
        ...s,
        items,
        total: Math.max(0, s.total - gone),
        selectedId: pickSelection(null, s.selectedId, items),
        checked: pruneChecked(s.checked, items),
      };
    });
  });

  const select = useCallback((id: string | null) => setState((s) => ({ ...s, selectedId: id })), []);
  const toggleChecked = useCallback(
    (id: string) =>
      setState((s) => {
        const next = new Set(s.checked);
        if (!next.delete(id)) next.add(id);
        return { ...s, checked: next };
      }),
    [],
  );
  const selectAll = useCallback(() => setState((s) => ({ ...s, checked: new Set(s.items.map((f) => f.fileID)) })), []);
  const clearChecked = useCallback(() => setState((s) => ({ ...s, checked: new Set() })), []);
  const toggleKind = useCallback((key: string) => setFilters((f) => toggleFilter(f, key)), []);
  const clearKinds = useCallback(() => setFilters([]), []);

  return {
    state,
    setState,
    latest,
    query,
    setQuery,
    filters,
    toggleKind,
    clearKinds,
    sort,
    setSort,
    uploading,
    setUploading,
    reload,
    loadMore,
    retry,
    select,
    toggleChecked,
    selectAll,
    clearChecked,
    hasActiveQuery: query.trim() !== "" || filters.length > 0,
  };
}

export type FileLibrary = ReturnType<typeof useFileLibrary>;
