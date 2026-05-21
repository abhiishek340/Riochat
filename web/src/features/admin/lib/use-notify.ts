"use client";

import { useMemo } from "react";
import { toast } from "@/components/ui/toast-store";
import { isAbortError } from "@/lib/api/errors";
import type { FormatArgs } from "@/lib/i18n/format";
import { useT, type MessageKey } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { problemText, type Problem } from "./problem";

/**
 * Toast helpers with the console's convention: the title states the outcome,
 * the description carries the resolved error message. Aborted requests are
 * never reported.
 */
export function useNotify() {
  const t = useT();
  const errorMessage = useErrorMessage();
  return useMemo(
    () => ({
      success: (key: MessageKey, args?: FormatArgs, description?: string) => toast.success(t(key, args), description ? { description } : undefined),
      info: (key: MessageKey, args?: FormatArgs, description?: string) => toast.info(t(key, args), description ? { description } : undefined),
      warning: (key: MessageKey, args?: FormatArgs, description?: string) => toast.warning(t(key, args), description ? { description } : undefined),
      /** `error` is the thrown value; its text becomes the description. */
      error: (key: MessageKey, error?: unknown, args?: FormatArgs) => {
        if (error !== undefined && isAbortError(error)) return;
        toast.error(t(key, args), error === undefined ? undefined : { description: errorMessage(error) });
      },
      /** A validation problem: title is the heading, the problem text is the description. */
      invalid: (heading: MessageKey, p: Problem) => toast.error(t(heading), { description: problemText(t, p) }),
      message: errorMessage,
    }),
    [t, errorMessage],
  );
}

export type Notify = ReturnType<typeof useNotify>;
