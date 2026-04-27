"use client";

import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/input";
import { useT } from "@/lib/i18n/provider";

export function pageCount(total: number, pageSize: number): number {
  return Math.max(1, Math.ceil(Math.max(0, total) / Math.max(1, pageSize)));
}

export function Pager({
  page,
  pageSize,
  total,
  sizes = [10, 25, 50, 100],
  disabled,
  onPage,
  onPageSize,
}: {
  page: number;
  pageSize: number;
  total: number;
  sizes?: number[];
  disabled?: boolean;
  onPage: (page: number) => void;
  onPageSize?: (size: number) => void;
}) {
  const t = useT();
  const count = pageCount(total, pageSize);
  return (
    <div className="flex flex-wrap items-center justify-between gap-3 text-sm text-muted-foreground">
      <span>{t("common.table.pagination", { page, pageCount: count, total })}</span>
      <div className="flex items-center gap-2">
        {onPageSize ? (
          <NativeSelect
            aria-label={t("settings.pager.pageSize")}
            className="h-8 w-auto pr-7 text-[13px]"
            value={pageSize}
            disabled={disabled}
            onChange={(e) => onPageSize(Number(e.target.value))}
          >
            {sizes.map((s) => (
              <option key={s} value={s}>
                {t("settings.pager.perPage", { count: s })}
              </option>
            ))}
          </NativeSelect>
        ) : null}
        <Button variant="outline" size="icon-sm" aria-label={t("common.table.previous")} disabled={disabled || page <= 1} onClick={() => onPage(page - 1)}>
          <ChevronLeft />
        </Button>
        <Button variant="outline" size="icon-sm" aria-label={t("common.table.next")} disabled={disabled || page >= count} onClick={() => onPage(page + 1)}>
          <ChevronRight />
        </Button>
      </div>
    </div>
  );
}
