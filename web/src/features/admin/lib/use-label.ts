"use client";

import { useCallback } from "react";
import { useI18n } from "@/lib/i18n/provider";

/**
 * Translates an enum value through `<prefix>.<value>`. Unknown values (the
 * server may add some) render as the raw text instead of failing.
 */
export function useLabel(): (prefix: string, value: string | null | undefined) => string {
  const { lookup } = useI18n();
  return useCallback(
    (prefix, value) => {
      const raw = (value ?? "").toString();
      if (!raw) return "-";
      return lookup(`${prefix}.${raw}`) ?? raw;
    },
    [lookup],
  );
}
