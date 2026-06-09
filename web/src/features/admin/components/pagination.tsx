"use client";

import { ChevronLeft, ChevronRight } from "lucide-react";
import { Button } from "@/components/ui/button";
import { NativeSelect } from "@/components/ui/input";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { pageCount, PAGE_SIZE_OPTIONS } from "../lib/paging";

export interface PaginationProps {
  page: number;
  pageSize: number;
  total: number;
  onPage: (page: number) => void;
  onPageSize?: (size: number) => void;
  sizes?: readonly number[];
  disabled?: boolean;
  className?: string;
}

/** Page summary, previous/next and (optionally) a page-size picker. */
export function Pagination({ page, pageSize, total, onPage, onPageSize, sizes = PAGE_SIZE_OPTIONS, disabled, className }: PaginationProps) {
  const t = useT();
  const pages = pageCount(total, pageSize);
  return (
    <div className={cn("flex flex-wrap items-center justify-between gap-3 text-sm text-muted-foreground", className)}>
      <span aria-live="polite">{t("common.table.pagination", { page, pageCount: pages, total })}</span>
      <div className="flex items-center gap-2">
        {onPageSize ? (
          <label className="flex items-center gap-2">
            <span className="hidden sm:inline">{t("adminCommon.table.rowsPerPage")}</span>
            <NativeSelect
              value={String(pageSize)}
              disabled={disabled}
              onChange={(event) => onPageSize(Number(event.target.value))}
              className="h-8 w-20"
              aria-label={t("adminCommon.table.rowsPerPage")}
            >
              {(sizes.includes(pageSize) ? sizes : [...sizes, pageSize].sort((a, b) => a - b)).map((size) => (
                <option key={size} value={size}>
                  {size}
                </option>
              ))}
            </NativeSelect>
          </label>
        ) : null}
        <Button variant="outline" size="icon-sm" disabled={disabled || page <= 1} onClick={() => onPage(page - 1)} aria-label={t("common.table.previous")}>
          <ChevronLeft />
        </Button>
        <Button variant="outline" size="icon-sm" disabled={disabled || page >= pages} onClick={() => onPage(page + 1)} aria-label={t("common.table.next")}>
          <ChevronRight />
        </Button>
      </div>
    </div>
  );
}
