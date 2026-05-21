"use client";

import { useRef } from "react";

/**
 * Keeps showing the last non-null value while a dialog animates closed, so its
 * content does not blank out mid-animation.
 */
export function useSnapshot<T>(value: T | null | undefined): T | null {
  const last = useRef<T | null>(null);
  if (value !== null && value !== undefined) last.current = value;
  return value ?? last.current;
}
