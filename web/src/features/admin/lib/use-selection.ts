"use client";

import { useCallback, useMemo, useState } from "react";
import { headerState, toggleKey, toggleMany, type CheckState } from "./selection";

export interface Selection<K> {
  selected: ReadonlySet<K>;
  count: number;
  has: (key: K) => boolean;
  toggle: (key: K, on: boolean) => void;
  /** Select or clear every key in `keys` (the selectable rows of the page). */
  toggleAll: (keys: readonly K[], on: boolean) => void;
  set: (keys: Iterable<K>) => void;
  clear: () => void;
  /** Drops keys that are no longer visible. */
  prune: (visible: readonly K[]) => void;
  headerState: (keys: readonly K[]) => CheckState;
}

export function useSelection<K = number>(): Selection<K> {
  const [selected, setSelected] = useState<ReadonlySet<K>>(new Set());
  const toggle = useCallback((key: K, on: boolean) => setSelected((s) => toggleKey(s, key, on)), []);
  const toggleAll = useCallback((keys: readonly K[], on: boolean) => setSelected((s) => toggleMany(s, keys, on)), []);
  const set = useCallback((keys: Iterable<K>) => setSelected(new Set(keys)), []);
  const clear = useCallback(() => setSelected((s) => (s.size === 0 ? s : new Set())), []);
  const prune = useCallback((visible: readonly K[]) => {
    setSelected((s) => {
      if (s.size === 0) return s;
      const keep = new Set(visible);
      let changed = false;
      const next = new Set<K>();
      for (const key of s) {
        if (keep.has(key)) next.add(key);
        else changed = true;
      }
      return changed ? next : s;
    });
  }, []);
  return useMemo(
    () => ({
      selected,
      count: selected.size,
      has: (key: K) => selected.has(key),
      toggle,
      toggleAll,
      set,
      clear,
      prune,
      headerState: (keys: readonly K[]) => headerState(selected, keys),
    }),
    [selected, toggle, toggleAll, set, clear, prune],
  );
}
