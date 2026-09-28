"use client";

import { Input } from "@/components/ui/input";
import { useT } from "@/lib/i18n/provider";

/** Two day pickers that bound a list by creation time. */
export function DateRangeFilter({ from, to, onFrom, onTo }: { from: string; to: string; onFrom: (value: string) => void; onTo: (value: string) => void }) {
  const t = useT();
  return (
    <div className="flex items-center gap-1.5">
      <Input type="date" value={from} max={to || undefined} onChange={(e) => onFrom(e.target.value)} aria-label={t("adminCommon.dates.startDate")} className="h-9 w-36" />
      <span aria-hidden className="text-muted-foreground">
        –
      </span>
      <Input type="date" value={to} min={from || undefined} onChange={(e) => onTo(e.target.value)} aria-label={t("adminCommon.dates.endDate")} className="h-9 w-36" />
    </div>
  );
}

export function userText(row: { userLabel?: string; userDisplayName?: string; username?: string; userUsername?: string; userID?: number }): string {
  return row.userLabel || row.userDisplayName || row.username || row.userUsername || (row.userID ? `#${row.userID}` : "-");
}
