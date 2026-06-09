"use client";

import { X } from "lucide-react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n/provider";

/** Shown above a table while rows are selected: the count, bulk controls, a clear button. */
export function BulkBar({ count, onClear, children }: { count: number; onClear: () => void; children: ReactNode }) {
  const t = useT();
  if (count === 0) return null;
  return (
    <div role="region" aria-label={t("adminCommon.table.bulkActions")} className="flex flex-wrap items-center gap-2 rounded-lg border bg-accent/50 px-3 py-2 text-sm">
      <span className="font-medium">{t("adminCommon.table.selected", { count })}</span>
      <div className="flex flex-wrap items-center gap-2">{children}</div>
      <Button variant="ghost" size="sm" className="ml-auto" onClick={onClear}>
        <X />
        {t("adminCommon.table.clearSelection")}
      </Button>
    </div>
  );
}
