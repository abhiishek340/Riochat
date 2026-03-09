"use client";

import { useCallback, useEffect, useState } from "react";
import { readBoolean, writeBoolean } from "@/lib/utils/storage";

/**
 * A boolean preference in localStorage. Starts at `fallback` (so static HTML
 * and hydration agree), adopts the stored value after mount, and swallows
 * storage failures.
 */
export function useStoredBoolean(key: string, fallback: boolean): [boolean, (next: boolean) => void] {
  const [value, setValue] = useState(fallback);
  useEffect(() => {
    setValue(readBoolean(key, fallback));
  }, [key, fallback]);
  const set = useCallback(
    (next: boolean) => {
      setValue(next);
      writeBoolean(key, next);
    },
    [key],
  );
  return [value, set];
}
