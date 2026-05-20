"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { isAbortError } from "@/lib/api/errors";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { DEFAULT_LIST_TTL_MS, mergeResults, normalizeQuery, SEARCH_DEBOUNCE_MS, type SearchItem } from "../lib/search-model";
import { searchConversations, type SearchPage } from "./search-api";

export const MAX_QUERY_LENGTH = 200;

/** The empty-query list is cached for a short while so reopening the dialog is instant. */
let defaultCache: { at: number; page: number; value: SearchPage } | null = null;

export function invalidateDefaultSearch(): void {
  defaultCache = null;
}

/** Forget everything cached for the previous account (sign-out, account switch). */
export function resetSearchCaches(): void {
  defaultCache = null;
}

export function useConversationSearch(open: boolean) {
  const [query, setQuery] = useState("");
  const normalized = normalizeQuery(query).slice(0, MAX_QUERY_LENGTH);
  const debounced = useDebouncedValue(normalized, SEARCH_DEBOUNCE_MS);
  const effective = normalized === "" ? "" : debounced;

  const [items, setItems] = useState<SearchItem[]>([]);
  const [hasMore, setHasMore] = useState(false);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [error, setError] = useState(false);
  const [moreError, setMoreError] = useState(false);
  const [retryCount, setRetryCount] = useState(0);

  const version = useRef(0);
  const controller = useRef<AbortController | null>(null);

  const cancel = useCallback(() => {
    version.current += 1;
    controller.current?.abort();
    controller.current = null;
  }, []);

  // First page for the current (debounced) query.
  useEffect(() => {
    if (!open) {
      cancel();
      setQuery("");
      setError(false);
      setMoreError(false);
      setLoading(false);
      setLoadingMore(false);
      // Restore the cached default list so the next opening starts populated.
      if (defaultCache && Date.now() - defaultCache.at < DEFAULT_LIST_TTL_MS) {
        setItems(defaultCache.value.results);
        setHasMore(defaultCache.value.hasMore);
        setPage(defaultCache.page);
      } else {
        // Nothing fresh to restore: do not keep showing results of the last query.
        setItems([]);
        setHasMore(false);
        setPage(1);
      }
      return;
    }
    cancel();
    setMoreError(false);
    setLoadingMore(false);
    if (effective === "" && defaultCache && Date.now() - defaultCache.at < DEFAULT_LIST_TTL_MS) {
      setItems(defaultCache.value.results);
      setHasMore(defaultCache.value.hasMore);
      setPage(defaultCache.page);
      setLoading(false);
      setError(false);
      return;
    }
    const mine = version.current;
    const ctl = new AbortController();
    controller.current = ctl;
    setLoading(true);
    setError(false);
    searchConversations({ page: 1, query: effective }, ctl.signal).then(
      (result) => {
        if (mine !== version.current) return;
        setItems(result.results);
        setHasMore(result.hasMore);
        setPage(1);
        setLoading(false);
        if (effective === "") defaultCache = { at: Date.now(), page: 1, value: result };
      },
      (e: unknown) => {
        if (mine !== version.current || isAbortError(e)) return;
        // Results of an earlier query must not stay on screen as if they answered this one.
        setItems([]);
        setHasMore(false);
        setLoading(false);
        setError(true);
      },
    );
    return () => {
      // Superseded or closed: anything still in flight is ignored.
      version.current += 1;
      ctl.abort();
    };
  }, [open, effective, retryCount, cancel]);

  const loadMore = useCallback(() => {
    if (!open || loading || loadingMore || !hasMore || moreError) return;
    const mine = version.current;
    const ctl = new AbortController();
    controller.current = ctl;
    setLoadingMore(true);
    searchConversations({ page: page + 1, query: effective }, ctl.signal).then(
      (result) => {
        if (mine !== version.current) return;
        setItems((current) => {
          const merged = mergeResults(current, result.results);
          if (effective === "") defaultCache = { at: Date.now(), page: page + 1, value: { results: merged, hasMore: result.hasMore } };
          return merged;
        });
        setHasMore(result.hasMore);
        setPage(page + 1);
        setLoadingMore(false);
      },
      (e: unknown) => {
        if (mine !== version.current || isAbortError(e)) return;
        setLoadingMore(false);
        setMoreError(true);
      },
    );
  }, [open, loading, loadingMore, hasMore, moreError, page, effective]);

  const retryFirst = useCallback(() => {
    invalidateDefaultSearch();
    setRetryCount((n) => n + 1);
  }, []);
  const retryMore = useCallback(() => setMoreError(false), []);

  return {
    query,
    setQuery,
    items,
    hasMore,
    loading: loading || normalized !== effective,
    loadingMore,
    error: error && items.length === 0,
    moreError,
    loadMore,
    retryFirst,
    retryMore,
  };
}
