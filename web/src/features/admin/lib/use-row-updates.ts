"use client";

import { useCallback, useRef, useState } from "react";
import type { MessageKey } from "@/lib/i18n/provider";
import { useNotify } from "./use-notify";

export interface RowUpdateOptions<T> {
  /** Toast title when the request fails. */
  failKey: MessageKey;
  /** Combine the server's answer with the optimistic row; default is the server row. */
  merge?: (optimistic: T, server: T) => T;
  onSuccess?: (server: T | void) => void;
  onFailure?: (error: unknown) => void;
}

/**
 * Inline edits: the row changes at once, the request follows, the server's row
 * replaces the guess on success and the exact previous row returns on failure.
 * A row that already has an edit in flight ignores further edits.
 */
export function useRowUpdates<T, K extends string | number>(options: {
  getKey: (row: T) => K;
  setRows: (update: (rows: T[]) => T[]) => void;
}) {
  const notify = useNotify();
  const { getKey, setRows } = options;
  const inFlight = useRef(new Set<K>());
  const [pending, setPending] = useState<ReadonlySet<K>>(new Set());

  const update = useCallback(
    async (row: T, change: (row: T) => T, request: () => Promise<T | void>, opts: RowUpdateOptions<T>): Promise<boolean> => {
      const key = getKey(row);
      if (inFlight.current.has(key)) return false;
      inFlight.current.add(key);
      setPending(new Set(inFlight.current));
      const guess = change(row);
      setRows((rows) => rows.map((r) => (getKey(r) === key ? guess : r)));
      try {
        const server = await request();
        if (server !== undefined && server !== null) {
          const next = opts.merge ? opts.merge(guess, server as T) : (server as T);
          setRows((rows) => rows.map((r) => (getKey(r) === key ? next : r)));
        }
        opts.onSuccess?.(server);
        return true;
      } catch (error) {
        setRows((rows) => rows.map((r) => (getKey(r) === key ? row : r)));
        notify.error(opts.failKey, error);
        opts.onFailure?.(error);
        return false;
      } finally {
        inFlight.current.delete(key);
        setPending(new Set(inFlight.current));
      }
    },
    [getKey, setRows, notify],
  );

  return { update, pending, isPending: (key: K) => pending.has(key) };
}
