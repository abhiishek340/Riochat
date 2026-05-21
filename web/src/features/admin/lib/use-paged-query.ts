"use client";

import { useCallback, useEffect, useMemo, useRef, useState, startTransition } from "react";
import { isAbortError } from "@/lib/api/errors";
import { DEFAULT_PAGE_SIZE, type PageResult } from "./paging";

export interface PagedFetchArgs<F> {
  page: number;
  pageSize: number;
  filters: F;
  signal: AbortSignal;
}

export interface PagedQueryOptions<T, F> {
  filters: F;
  fetch: (args: PagedFetchArgs<F>) => Promise<PageResult<T>>;
  enabled?: boolean;
  initialPageSize?: number;
  onError?: (error: unknown) => void;
}

export interface PagedQuery<T> {
  rows: T[];
  total: number;
  page: number;
  pageSize: number;
  /** First load (nothing to show yet). */
  loading: boolean;
  /** Any request in flight; existing rows stay visible. */
  fetching: boolean;
  /** True while the newest request has failed (cleared by the next attempt). */
  error: boolean;
  /** Counts the answers that replaced the rows; local edits do not change it. */
  loadId: number;
  setPage: (page: number) => void;
  setPageSize: (size: number) => void;
  reload: () => void;
  /** Local edits (optimistic updates, removals). */
  setRows: (update: (rows: T[]) => T[]) => void;
  setTotal: (update: (total: number) => number) => void;
}

/**
 * A server-paged list. Changing filters or page size returns to page 1, only
 * the newest response may update the screen, and rows stay visible while a
 * refresh is in flight.
 */
export function usePagedQuery<T, F>(options: PagedQueryOptions<T, F>): PagedQuery<T> {
  const { filters, enabled = true } = options;
  const filtersKey = useMemo(() => JSON.stringify(filters), [filters]);
  const latest = useRef(options);
  latest.current = options;

  const [pageSize, setPageSizeState] = useState(options.initialPageSize ?? DEFAULT_PAGE_SIZE);
  const [pageState, setPageState] = useState({ key: filtersKey, page: 1, size: pageSize });
  const page = pageState.key === filtersKey && pageState.size === pageSize ? pageState.page : 1;

  const [rows, setRowsState] = useState<T[]>([]);
  const [total, setTotalState] = useState(0);
  const [fetching, setFetching] = useState(false);
  const [loadedOnce, setLoadedOnce] = useState(false);
  const [failed, setFailed] = useState(false);
  const [loadId, setLoadId] = useState(0);
  const [nonce, setNonce] = useState(0);
  const requestId = useRef(0);

  useEffect(() => {
    if (!enabled) return;
    const controller = new AbortController();
    const id = ++requestId.current;
    setFetching(true);
    setFailed(false);
    latest.current
      .fetch({ page, pageSize, filters: latest.current.filters, signal: controller.signal })
      .then((result) => {
        if (id !== requestId.current) return;
        startTransition(() => {
          setRowsState(result.results);
          setTotalState(result.total);
          setLoadedOnce(true);
          setFailed(false);
          setLoadId((n) => n + 1);
        });
      })
      .catch((error: unknown) => {
        if (id !== requestId.current || isAbortError(error)) return;
        setLoadedOnce(true);
        setFailed(true);
        latest.current.onError?.(error);
      })
      .finally(() => {
        if (id === requestId.current) setFetching(false);
      });
    return () => controller.abort();
  }, [enabled, filtersKey, page, pageSize, nonce]);

  const setPage = useCallback(
    (next: number) => setPageState({ key: filtersKey, page: Math.max(1, Math.floor(next)), size: pageSize }),
    [filtersKey, pageSize],
  );
  const setPageSize = useCallback((size: number) => setPageSizeState(size), []);
  const reload = useCallback(() => setNonce((n) => n + 1), []);
  const setRows = useCallback((update: (rows: T[]) => T[]) => setRowsState((r) => update(r)), []);
  const setTotal = useCallback((update: (n: number) => number) => setTotalState((n) => Math.max(0, update(n))), []);

  return { rows, total, page, pageSize, loading: !loadedOnce && enabled, fetching, error: failed, loadId, setPage, setPageSize, reload, setRows, setTotal };
}
