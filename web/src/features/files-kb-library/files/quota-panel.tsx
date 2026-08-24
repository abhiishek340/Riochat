"use client";

import { HardDrive } from "lucide-react";
import { useT } from "@/lib/i18n/provider";
import { describeQuota, formatBytes } from "../shared/format";
import type { StorageQuota } from "../shared/types";

/** Storage usage at the bottom of the file list. */
export function QuotaPanel({ quota }: { quota: StorageQuota }) {
  const t = useT();
  const view = describeQuota(quota);
  const used = formatBytes(view.used);
  return (
    <section aria-label={t("filesWs.quota.title")} className="grid gap-2 border-t bg-muted/30 px-4 py-3 text-xs">
      <div className="flex items-center justify-between gap-2">
        <span className="flex items-center gap-1.5 font-medium text-foreground">
          <HardDrive aria-hidden className="size-3.5 text-muted-foreground" />
          {t("filesWs.quota.title")}
        </span>
        <span className="tabular-nums text-muted-foreground">
          {t("filesWs.quota.usage", { used, total: view.unlimited ? t("filesWs.quota.unlimited") : formatBytes(view.total) })}
        </span>
      </div>
      {view.unlimited ? (
        <p className="text-muted-foreground">{t("filesWs.quota.noCap")}</p>
      ) : (
        <>
          <div
            role="progressbar"
            aria-label={t("filesWs.quota.title")}
            aria-valuetext={t("filesWs.quota.percentUsed", { percent: Math.round(view.percent) })}
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.round(view.percent)}
            className="h-1.5 overflow-hidden rounded-full bg-border"
          >
            <div className="h-full rounded-full bg-primary transition-[width] duration-300" style={{ width: `${view.percent}%` }} />
          </div>
          <div className="flex justify-between text-muted-foreground">
            <span>{t("filesWs.quota.percentUsed", { percent: Math.round(view.percent) })}</span>
            <span>{t("filesWs.quota.left", { remaining: formatBytes(view.remaining) })}</span>
          </div>
        </>
      )}
    </section>
  );
}
