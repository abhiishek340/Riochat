/** Selection state helpers; pure so they can be unit-tested. */

export function toggleKey<K>(selected: ReadonlySet<K>, key: K, on: boolean): Set<K> {
  const next = new Set(selected);
  if (on) next.add(key);
  else next.delete(key);
  return next;
}

/** Adds (or removes) a whole batch of keys at once. */
export function toggleMany<K>(selected: ReadonlySet<K>, keys: readonly K[], on: boolean): Set<K> {
  const next = new Set(selected);
  for (const key of keys) {
    if (on) next.add(key);
    else next.delete(key);
  }
  return next;
}

export type CheckState = boolean | "indeterminate";

/** Header checkbox state for a set of selectable keys. */
export function headerState<K>(selected: ReadonlySet<K>, keys: readonly K[]): CheckState {
  if (keys.length === 0) return false;
  let count = 0;
  for (const key of keys) if (selected.has(key)) count++;
  if (count === 0) return false;
  return count === keys.length ? true : "indeterminate";
}
