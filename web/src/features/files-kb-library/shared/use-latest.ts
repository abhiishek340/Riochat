"use client";

import { useLayoutEffect, useRef, type MutableRefObject } from "react";

/**
 * Keeps `value` in a ref that is up to date before any passive effect of the
 * same commit runs, so callbacks fired from a child's effect never read the
 * previous render's state.
 */
export function useLatest<T>(value: T): MutableRefObject<T> {
  const ref = useRef(value);
  useLayoutEffect(() => {
    ref.current = value;
  });
  return ref;
}
