"use client";

import { useCallback } from "react";
import { formatError } from "./errors";
import { useI18n } from "./provider";

/** `const msg = useErrorMessage(); toast.error(msg(err))` */
export function useErrorMessage(): (error: unknown, fallback?: string) => string {
  const { locale, lookup } = useI18n();
  return useCallback((error, fallback) => formatError(error, { locale, lookup }, fallback), [locale, lookup]);
}
