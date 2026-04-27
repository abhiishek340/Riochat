"use client";

import { AlertCircle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";

/**
 * Inline notice for a page whose data could not be loaded: what failed, the
 * localized reason, and a button to try again. Announced to assistive tech.
 */
export function LoadErrorNotice({ title, error, onRetry, retrying = false }: { title: string; error?: unknown; onRetry: () => void; retrying?: boolean }) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const reason = error === undefined ? "" : errorMessage(error);
  return (
    <div role="alert" className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-destructive/30 bg-destructive/8 px-4 py-3">
      <div className="flex min-w-0 items-start gap-2.5">
        <AlertCircle className="mt-0.5 size-4 shrink-0 text-destructive" aria-hidden />
        <div className="grid min-w-0 gap-0.5">
          <span className="text-sm font-medium">{title}</span>
          {reason ? <span className="text-[13px] text-muted-foreground">{reason}</span> : null}
        </div>
      </div>
      <Button variant="outline" size="sm" loading={retrying} onClick={onRetry}>
        {t("common.actions.retry")}
      </Button>
    </div>
  );
}
