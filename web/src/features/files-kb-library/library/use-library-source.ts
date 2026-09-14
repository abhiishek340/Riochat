"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "@/components/ui";
import { isAbortError } from "@/lib/api/errors";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { hasSession } from "../shared/session";
import { useLatest } from "../shared/use-latest";
import { LIBRARY_PAGE_SIZE, listMine, listVisible, type ItemPage } from "./api";
import { uniqueBy } from "../shared/async";
import type { LibraryItem, LibraryKind } from "./model";

export interface LibrarySource {
  mine: LibraryItem[];
  builtin: LibraryItem[];
  loading: boolean;
  loadingMore: boolean;
  hasMore: boolean;
  /** A page failed to load: automatic loading stops until `retry`. */
  moreFailed: boolean;
  /** The first page failed to load (nothing to show yet). */
  failed: boolean;
  /** Starts over from page 1 (the "try again" of a failed first load). */
  reload: () => void;
  loadMore: () => void;
  retry: () => void;
  mutateMine: (fn: (prev: LibraryItem[]) => LibraryItem[]) => void;
}

interface Stream {
  items: LibraryItem[];
  page: number;
  total: number;
}
const EMPTY_STREAM: Stream = { items: [], page: 0, total: 0 };
const more = (s: Stream) => s.page > 0 && s.page * LIBRARY_PAGE_SIZE < s.total;
const append = (s: Stream, p: ItemPage, page: number): Stream => ({
  items: uniqueBy([...s.items, ...p.items], (i) => `${i.scope}:${i.id}`),
  page,
  total: p.total,
});

/**
 * Loads the user's own items and the visible (built-in) ones. With
 * `serverSearch` the debounced query is sent as `q` and both streams restart
 * from page 1 whenever it changes; otherwise only page 1 is read and search is
 * left to the caller.
 */
export function useLibrarySource(kind: LibraryKind, query: string, serverSearch: boolean): LibrarySource {
  const t = useT();
  const errorMessage = useErrorMessage();
  const debounced = useDebouncedValue(query, 250);
  const q = serverSearch ? debounced : "";
  const [mine, setMine] = useState<Stream>(EMPTY_STREAM);
  const [builtin, setBuiltin] = useState<Stream>(EMPTY_STREAM);
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [moreFailed, setMoreFailed] = useState(false);
  const [failed, setFailed] = useState(false);
  const run = useRef(0);
  const latest = useLatest({ mine, builtin, loading, loadingMore, moreFailed, t, errorMessage });

  const loadFirst = useCallback(
    async (signal: AbortSignal, id: number) => {
      setLoading(true);
      setMoreFailed(false);
      setFailed(false);
      try {
        if (!(await hasSession())) {
          if (id === run.current) {
            setMine(EMPTY_STREAM);
            setBuiltin(EMPTY_STREAM);
          }
          return;
        }
        const [a, b] = await Promise.all([
          listMine(kind, { page: 1, pageSize: LIBRARY_PAGE_SIZE, q }, signal),
          listVisible(kind, { page: 1, pageSize: LIBRARY_PAGE_SIZE, q }, signal),
        ]);
        if (id !== run.current) return;
        setMine(append(EMPTY_STREAM, a, 1));
        setBuiltin(append(EMPTY_STREAM, b, 1));
      } catch (error) {
        if (isAbortError(error) || id !== run.current) return;
        setFailed(true);
        const { t: tr, errorMessage: em } = latest.current;
        toast.error(tr(`libraryWs.${kind}.loadFailed`), { id: `library-${kind}-load`, description: em(error) });
      } finally {
        if (id === run.current) setLoading(false);
      }
    },
    [kind, q, latest],
  );

  const controller = useRef<AbortController | null>(null);
  const restart = useCallback(() => {
    const id = ++run.current;
    controller.current?.abort();
    const ctrl = new AbortController();
    controller.current = ctrl;
    void loadFirst(ctrl.signal, id);
  }, [loadFirst]);

  useEffect(() => {
    restart();
    return () => {
      run.current += 1;
      controller.current?.abort();
    };
  }, [restart]);

  const loadMore = useCallback(() => {
    const cur = latest.current;
    if (!serverSearch || cur.loading || cur.loadingMore || cur.moreFailed) return;
    const target = more(cur.mine) ? "mine" : more(cur.builtin) ? "builtin" : null;
    if (!target) return;
    const id = run.current;
    const stream = target === "mine" ? cur.mine : cur.builtin;
    const page = stream.page + 1;
    setLoadingMore(true);
    const fetchPage = target === "mine" ? listMine : listVisible;
    fetchPage(kind, { page, pageSize: LIBRARY_PAGE_SIZE, q }).then(
      (res) => {
        if (id !== run.current) return;
        (target === "mine" ? setMine : setBuiltin)((s) => append(s, res, page));
        setLoadingMore(false);
      },
      (error: unknown) => {
        if (id !== run.current) return;
        setLoadingMore(false);
        setMoreFailed(true);
        toast.error(cur.t(`libraryWs.${kind}.loadFailed`), { id: `library-${kind}-more`, description: cur.errorMessage(error) });
      },
    );
  }, [kind, q, serverSearch, latest]);

  const retry = useCallback(() => {
    setMoreFailed(false);
    latest.current.moreFailed = false;
    loadMore();
  }, [loadMore, latest]);

  const mutateMine = useCallback((fn: (prev: LibraryItem[]) => LibraryItem[]) => setMine((s) => ({ ...s, items: fn(s.items) })), []);

  return {
    mine: mine.items,
    builtin: builtin.items,
    loading,
    loadingMore,
    hasMore: serverSearch && (more(mine) || more(builtin)),
    moreFailed,
    failed,
    reload: restart,
    loadMore,
    retry,
    mutateMine,
  };
}
