"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "@/components/ui";
import { isAbortError } from "@/lib/api/errors";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { uniqueBy } from "../shared/async";
import { subscribeKbChanged } from "../shared/invalidation";
import { hasSession } from "../shared/session";
import { useLatest } from "../shared/use-latest";
import type { KnowledgeBase } from "../shared/types";
import { getKnowledgeBase, listKnowledgeBases, type KbMode, type KbSort } from "./api";
import { LIST_PAGE_SIZE, mergeKbPage, pruneSelection, refreshPages } from "./model";

export interface KbListState {
  items: KnowledgeBase[];
  total: number;
  page: number;
  /** True only before the first answer, or while a non-silent reload runs. */
  loading: boolean;
  loadingMore: boolean;
  selectedId: string | null;
  checked: Set<string>;
  /** The last visible (non-silent) load failed; cleared by the next attempt. */
  loadFailed: boolean;
}

const INITIAL: KbListState = { items: [], total: 0, page: 1, loading: true, loadingMore: false, selectedId: null, checked: new Set(), loadFailed: false };
const REFRESH_THROTTLE_MS = 250;

interface LoadOptions {
  preferred?: string | null;
  silent?: boolean;
}

/** The list of knowledge bases: search, sort, paging, selection and the refresh triggers. */
export function useKbList(mode: KbMode, enabled: boolean) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [state, setState] = useState<KbListState>(INITIAL);
  const [query, setQuery] = useState("");
  const [sort, setSort] = useState<KbSort>("default");
  const debounced = useDebouncedValue(query, 200);

  const latest = useLatest({ state, debounced, sort, mode, t, errorMessage });
  const version = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const lastRefresh = useRef(0);

  const load = useCallback(async ({ preferred = null, silent = false }: LoadOptions = {}) => {
    const run = ++version.current;
    controller.current?.abort();
    const ctrl = new AbortController();
    controller.current = ctrl;
    const cur = latest.current;
    if (!silent) setState((s) => ({ ...s, loading: true, loadFailed: false }));
    try {
      if (!(await hasSession())) {
        if (run === version.current) setState((s) => ({ ...s, items: [], total: 0, loading: false, selectedId: null, checked: new Set() }));
        return;
      }
      const res = await listKnowledgeBases(cur.mode, { page: 1, pageSize: LIST_PAGE_SIZE, q: cur.debounced, sort: cur.sort }, ctrl.signal);
      let items = res.results;
      const ensure = preferred ?? (silent ? cur.state.selectedId : null);
      // A visible reload (new search or sort) still keeps the current base when it is among the results.
      const keep = ensure ?? cur.state.selectedId;
      if (ensure && !items.some((kb) => kb.publicID === ensure)) {
        try {
          const one = await getKnowledgeBase(cur.mode, ensure, ctrl.signal);
          if (one) items = [one, ...items];
        } catch (error) {
          if (isAbortError(error)) throw error;
          /* the base is gone or not visible: ignore */
        }
      }
      if (run !== version.current) return;
      setState((s) => ({
        items,
        total: res.total,
        page: 1,
        loading: false,
        loadingMore: false,
        selectedId: keep && items.some((kb) => kb.publicID === keep) ? keep : (items[0]?.publicID ?? null),
        checked: pruneSelection(s.checked, items, cur.mode),
        loadFailed: false,
      }));
    } catch (error) {
      if (isAbortError(error) || run !== version.current) return;
      setState((s) => ({ ...s, loading: false, loadFailed: !silent }));
      if (!silent) toast.error(cur.t("kbWs.toasts.loadFailed"), { id: "kb-load-failed", description: cur.errorMessage(error) });
    }
  }, [latest]);

  // Reload page 1 when the search, sort or mode changes.
  useEffect(() => {
    if (!enabled) return;
    void load();
  }, [enabled, debounced, sort, mode, load]);

  useEffect(
    () => () => {
      version.current += 1;
      controller.current?.abort();
    },
    [],
  );

  const loadMore = useCallback(async () => {
    const cur = latest.current;
    if (cur.state.loading || cur.state.loadingMore || cur.state.items.length >= cur.state.total) return;
    const run = version.current;
    const page = cur.state.page + 1;
    setState((s) => ({ ...s, loadingMore: true }));
    try {
      const res = await listKnowledgeBases(cur.mode, { page, pageSize: LIST_PAGE_SIZE, q: cur.debounced, sort: cur.sort });
      if (run !== version.current) return;
      setState((s) => {
        const items = mergeKbPage(s.items, res.results);
        return { ...s, items, total: res.total, page, loadingMore: false };
      });
    } catch (error) {
      if (run !== version.current) return;
      setState((s) => ({ ...s, loadingMore: false }));
      toast.error(cur.t("kbWs.toasts.loadFailed"), { id: "kb-load-failed", description: cur.errorMessage(error) });
    }
  }, [latest]);

  /** Re-reads everything loaded so far, keeping the selection. Silent and throttled. */
  const refresh = useCallback(async () => {
    const now = Date.now();
    if (now - lastRefresh.current < REFRESH_THROTTLE_MS) return;
    lastRefresh.current = now;
    const cur = latest.current;
    if (cur.state.loading || cur.state.items.length === 0) return;
    const run = ++version.current;
    controller.current?.abort();
    const ctrl = new AbortController();
    controller.current = ctrl;
    try {
      const pages = refreshPages(cur.state.items.length);
      const answers = await Promise.all(
        pages.map((page) => listKnowledgeBases(cur.mode, { page, pageSize: 100, q: cur.debounced, sort: cur.sort }, ctrl.signal)),
      );
      let items = uniqueBy(answers.flatMap((a) => a.results), (kb) => kb.publicID);
      const total = answers[0]?.total ?? items.length;
      const selected = cur.state.selectedId;
      if (selected && !items.some((kb) => kb.publicID === selected)) {
        const one = await getKnowledgeBase(cur.mode, selected, ctrl.signal).catch((e: unknown) => {
          if (isAbortError(e)) throw e;
          return null;
        });
        if (one) items = [one, ...items];
      }
      if (run !== version.current) return;
      setState((s) => ({
        ...s,
        items,
        total,
        page: Math.max(1, Math.ceil(items.length / LIST_PAGE_SIZE)),
        selectedId: selected && items.some((kb) => kb.publicID === selected) ? selected : (items[0]?.publicID ?? null),
        checked: pruneSelection(s.checked, items, cur.mode),
      }));
    } catch {
      /* silent */
    }
  }, [latest]);

  // Refresh when the user comes back to this tab or another tab changed something.
  useEffect(() => {
    if (!enabled) return;
    const onFocus = () => void refresh();
    const onVisible = () => document.visibilityState === "visible" && void refresh();
    window.addEventListener("focus", onFocus);
    document.addEventListener("visibilitychange", onVisible);
    const off = subscribeKbChanged(() => void refresh());
    return () => {
      window.removeEventListener("focus", onFocus);
      document.removeEventListener("visibilitychange", onVisible);
      off();
    };
  }, [enabled, refresh]);

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
  const setChecked = useCallback((ids: Iterable<string>) => setState((s) => ({ ...s, checked: new Set(ids) })), []);
  const replaceItem = useCallback(
    (kb: KnowledgeBase) => setState((s) => ({ ...s, items: s.items.map((x) => (x.publicID === kb.publicID ? kb : x)) })),
    [],
  );

  return { state, setState, query, setQuery, sort, setSort, load, loadMore, refresh, select, toggleChecked, setChecked, replaceItem };
}

export type KbList = ReturnType<typeof useKbList>;
