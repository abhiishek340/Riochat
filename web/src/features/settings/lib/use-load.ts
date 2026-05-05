"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { isAbortError } from "@/lib/api/errors";

export interface LoadState<T> {
  data: T | undefined;
  error: unknown;
  loading: boolean;
  reload: () => void;
  /** Replaces the loaded value locally (after a mutation) without refetching. */
  setData: (next: T | ((prev: T | undefined) => T | undefined)) => void;
}

/**
 * Runs `fetcher` on mount and whenever `key` changes; `reload` runs it again.
 * A superseded or unmounted request never writes its result.
 * `enabled = false` keeps the state idle.
 */
export function useLoad<T>(fetcher: (signal: AbortSignal) => Promise<T>, key: unknown, enabled = true): LoadState<T> {
  const [data, setDataState] = useState<T | undefined>(undefined);
  const [error, setError] = useState<unknown>(undefined);
  const [loading, setLoading] = useState(enabled);
  const [tick, setTick] = useState(0);
  // The (key, tick) pair whose request has finished. Until it matches the
  // current pair no answer for it exists yet, even in the render that sees
  // `enabled` flip to true (before the effect has started the request).
  const [settled, setSettled] = useState<{ key: unknown; tick: number } | null>(null);
  const fetcherRef = useRef(fetcher);
  fetcherRef.current = fetcher;

  useEffect(() => {
    if (!enabled) {
      setLoading(false);
      setSettled(null);
      return;
    }
    const controller = new AbortController();
    setLoading(true);
    setError(undefined);
    fetcherRef.current(controller.signal).then(
      (value) => {
        if (controller.signal.aborted) return;
        setDataState(value);
        setLoading(false);
        setSettled({ key, tick });
      },
      (e: unknown) => {
        if (controller.signal.aborted || isAbortError(e)) return;
        setError(e);
        setLoading(false);
        setSettled({ key, tick });
      },
    );
    return () => controller.abort();
  }, [enabled, key, tick]);

  const reload = useCallback(() => setTick((n) => n + 1), []);
  const setData = useCallback((next: T | ((prev: T | undefined) => T | undefined)) => {
    setDataState((prev) => (typeof next === "function" ? (next as (p: T | undefined) => T | undefined)(prev) : next));
  }, []);
  const pending = enabled && (loading || settled === null || !Object.is(settled.key, key) || settled.tick !== tick);
  return { data, error, loading: pending, reload, setData };
}
