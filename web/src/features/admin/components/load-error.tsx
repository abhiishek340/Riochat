"use client";

import { AlertTriangle } from "lucide-react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";

/** Inline notice for data that could not be read, with a way to try again. */
export function LoadError({ message, onRetry, className }: { message?: ReactNode; onRetry?: () => void; className?: string }) {
  const t = useT();
  return (
    <div role="alert" className={cn("flex flex-col items-center gap-3 rounded-lg border border-destructive/30 bg-destructive/5 px-4 py-6 text-center", className)}>
      <span className="flex items-center gap-2 text-sm font-medium text-destructive">
        <AlertTriangle className="size-4 shrink-0" aria-hidden />
        {message ?? t("adminCommon.page.loadFailed")}
      </span>
      {onRetry ? (
        <Button variant="outline" size="sm" onClick={onRetry}>
          {t("adminCommon.table.retry")}
        </Button>
      ) : null}
    </div>
  );
}
