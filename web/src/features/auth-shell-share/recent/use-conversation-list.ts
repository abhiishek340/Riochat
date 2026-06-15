"use client";

import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { toast } from "@/components/ui/toast-store";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { subscribeConversationChanges, type ConversationChange } from "../lib/conversation-events";
import { fetchConversationPage } from "./api";
import { filtersKey, hasMorePages, matchesFilters, mergePage, sortByUpdated, type ConversationRow, type Filters } from "./model";

const LOAD_MORE_TOAST = "recent-load-more";

/**
 * The page's conversation list. Every filter change starts over; responses
 * that belong to an earlier filter are dropped by a version counter, so a slow
 * request can never overwrite a newer list.
 */
export function useConversationList(filters: Filters) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const key = filtersKey(filters);
  const filtersRef = useRef(filters);
  filtersRef.current = filters;

  const [rows, setRows] = useState<ConversationRow[]>([]);
  const [loaded, setLoaded] = useState(false);
  const [error, setError] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreError, setMoreError] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const [reloadCount, setReloadCount] = useState(0);

  const rowsRef = useRef<ConversationRow[]>([]);
  rowsRef.current = rows;
  const version = useRef(0);
  const state = useRef({ page: 1, total: 0 });
  const controller = useRef<AbortController | null>(null);

  useEffect(() => {
    const mine = ++version.current;
    controller.current?.abort();
    const ctl = new AbortController();
    controller.current = ctl;
    setRows([]);
    setLoaded(false);
    setError(false);
    setMoreError(false);
    setLoadingMore(false);
    setHasMore(false);
    toast.dismiss(LOAD_MORE_TOAST);
    fetchConversationPage(filtersRef.current, 1, ctl.signal).then(
      (page) => {
        if (mine !== version.current) return;
        const sorted = sortByUpdated(page.rows);
        state.current = { page: 1, total: page.total };
        setRows(sorted);
        setHasMore(hasMorePages(page.received, sorted.length, page.total));
        setLoaded(true);
      },
      (e: unknown) => {
        if (mine !== version.current || isAbortError(e)) return;
        setError(true);
        setLoaded(true);
      },
    );
    return () => ctl.abort();
  }, [key, reloadCount]);

  const loadMore = useCallback(() => {
    if (!loaded || loadingMore || !hasMore || moreError) return;
    const mine = version.current;
    const next = state.current.page + 1;
    setLoadingMore(true);
    fetchConversationPage(filtersRef.current, next, controller.current?.signal).then(
      (page) => {
        if (mine !== version.current) return;
        state.current = { page: next, total: page.total };
        const merged = mergePage(rowsRef.current, page.rows);
        setRows(merged);
        setHasMore(hasMorePages(page.received, merged.length, page.total));
        setLoadingMore(false);
      },
      (e: unknown) => {
        if (mine !== version.current || isAbortError(e)) return;
        setLoadingMore(false);
        setMoreError(true);
        toast.error(t("recentAll.list.moreFailedTitle"), { id: LOAD_MORE_TOAST, description: errorMessage(e), duration: 0 });
      },
    );
  }, [loaded, loadingMore, hasMore, moreError, t, errorMessage]);

  const retryMore = useCallback(() => {
    toast.dismiss(LOAD_MORE_TOAST);
    setMoreError(false);
  }, []);
  const reload = useCallback(() => setReloadCount((n) => n + 1), []);

  /** Merge a change into the list, then drop the row if it no longer matches the filters. */
  const patch = useCallback((id: string, changes: Partial<ConversationRow>) => {
    setRows((current) =>
      current.flatMap((row) => {
        if (row.publicID !== id) return [row];
        const next = { ...row, ...changes };
        return matchesFilters(next, filtersRef.current) ? [next] : [];
      }),
    );
  }, []);
  const remove = useCallback((ids: readonly string[]) => {
    const gone = new Set(ids);
    setRows((current) => current.filter((r) => !gone.has(r.publicID)));
  }, []);

  // Changes announced elsewhere (sidebar, chat header).
  useEffect(
    () =>
      subscribeConversationChanges((change: ConversationChange) => {
        if (change.type === "remove") remove(change.ids);
        else if (change.type === "patch") patch(change.id, change.patch);
        else {
          const filtersNow = filtersRef.current;
          if (!matchesFilters(change.row, filtersNow)) remove([change.row.publicID]);
          else if (filtersNow.query === "") {
            setRows((current) =>
              current.some((r) => r.publicID === change.row.publicID)
                ? current.map((r) => (r.publicID === change.row.publicID ? change.row : r))
                : sortByUpdated([...current, change.row]),
            );
          } else {
            // With a text query active, only rows already listed are updated.
            setRows((current) => current.map((r) => (r.publicID === change.row.publicID ? change.row : r)));
          }
        }
      }),
    [patch, remove],
  );

  return useMemo(
    () => ({ rows, loaded, error, loadingMore, moreError, hasMore, loadMore, retryMore, reload, patch, remove }),
    [rows, loaded, error, loadingMore, moreError, hasMore, loadMore, retryMore, reload, patch, remove],
  );
}
