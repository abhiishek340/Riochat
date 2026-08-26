"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "@/components/ui";
import { isAbortError } from "@/lib/api/errors";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { uniqueBy } from "../shared/async";
import { useLatest } from "../shared/use-latest";
import type { KnowledgeBaseFile } from "../shared/types";
import { listAvailableFiles, type KbMode } from "./api";
import { AVAILABLE_PAGE_SIZE } from "./model";

/** The files that could still be added to a base (platform sources in admin mode). */
export function useAvailableFiles(mode: KbMode, kbId: string, open: boolean, query: string) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const debounced = useDebouncedValue(query, 200);
  const [items, setItems] = useState<KnowledgeBaseFile[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(false);
  const [loadingMore, setLoadingMore] = useState(false);
  const [failed, setFailed] = useState(false);
  const run = useRef(0);
  const controller = useRef<AbortController | null>(null);
  const text = useLatest({ t, errorMessage });

  const fetchPage = useCallback(
    async (target: number, q: string) => {
      const id = ++run.current;
      controller.current?.abort();
      const ctrl = new AbortController();
      controller.current = ctrl;
      if (target === 1) {
        setLoading(true);
        setFailed(false);
      } else setLoadingMore(true);
      try {
        const res = await listAvailableFiles(mode, kbId, { page: target, pageSize: AVAILABLE_PAGE_SIZE, q }, ctrl.signal);
        if (id !== run.current) return;
        setItems((prev) => (target === 1 ? res.results : uniqueBy([...prev, ...res.results], (f) => f.fileID)));
        setTotal(res.total);
        setPage(target);
      } catch (error) {
        if (isAbortError(error) || id !== run.current) return;
        if (target === 1) {
          setFailed(true);
          setItems([]);
          setTotal(0);
        }
        toast.error(text.current.t("kbWs.toasts.loadFailed"), { id: "kb-available-failed", description: text.current.errorMessage(error) });
      } finally {
        if (id === run.current) {
          setLoading(false);
          setLoadingMore(false);
        }
      }
    },
    [mode, kbId, text],
  );

  useEffect(() => {
    if (!open) {
      run.current += 1;
      controller.current?.abort();
      setItems([]);
      setTotal(0);
      setPage(1);
      setFailed(false);
      return;
    }
    void fetchPage(1, debounced);
    return () => {
      run.current += 1;
      controller.current?.abort();
    };
  }, [open, debounced, fetchPage]);

  const loadMore = useCallback(() => {
    if (!loading && !loadingMore && items.length < total) void fetchPage(page + 1, debounced);
  }, [loading, loadingMore, items.length, total, page, debounced, fetchPage]);

  const retry = useCallback(() => void fetchPage(1, debounced), [fetchPage, debounced]);

  const removeLocal = useCallback((fileID: string) => {
    setItems((prev) => prev.filter((f) => f.fileID !== fileID));
    setTotal((n) => Math.max(0, n - 1));
  }, []);

  return { items, total, loading, loadingMore, failed, loadMore, retry, removeLocal };
}
