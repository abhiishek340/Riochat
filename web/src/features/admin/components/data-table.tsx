"use client";

import { Fragment, type ReactNode } from "react";
import { AlertTriangle } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import {
  Table,
  TableBody,
  TableCell,
  TableEmptyRow,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import type { Selection } from "../lib/use-selection";
import { Checkbox } from "./checkbox";

export interface Column<T> {
  id: string;
  header: ReactNode;
  cell: (row: T, index: number) => ReactNode;
  className?: string;
  headClassName?: string;
  /** Sticks the column to a horizontal edge while the table scrolls. */
  sticky?: "left" | "right";
}

export interface DataTableProps<T, K extends string | number> {
  columns: readonly Column<T>[];
  rows: readonly T[];
  rowKey: (row: T) => K;
  loading?: boolean;
  /** A refresh is running over rows that are already shown. */
  fetching?: boolean;
  selection?: Selection<K>;
  /** Rows that cannot be selected (for example protected accounts). */
  selectable?: (row: T) => boolean;
  onRowClick?: (row: T) => void;
  rowTone?: (row: T) => "muted" | "warning" | undefined;
  empty?: ReactNode;
  /** The newest request failed; with no rows to show this replaces the empty row. */
  error?: boolean;
  onRetry?: () => void;
  /** Names a row for assistive technology (select checkbox). */
  rowLabel?: (row: T) => string;
  /** Accessible name for the table. */
  label?: string;
  skeletonRows?: number;
  maxHeight?: string;
  /** Content shown in a full-width row under a row; return null for rows that are not expanded. */
  renderExpanded?: (row: T) => ReactNode;
}

const STICKY = {
  left: "sticky left-0 z-[1] bg-card",
  right: "sticky right-0 z-[1] bg-card",
} as const;

/** The console's list table: optional selection column, sticky edges, loading and empty rows. */
export function DataTable<T, K extends string | number>({
  columns,
  rows,
  rowKey,
  loading,
  fetching,
  selection,
  selectable,
  onRowClick,
  rowTone,
  empty,
  error,
  onRetry,
  rowLabel,
  label,
  skeletonRows = 6,
  maxHeight,
  renderExpanded,
}: DataTableProps<T, K>) {
  const t = useT();
  const span = columns.length + (selection ? 1 : 0);
  const selectableKeys = selection
    ? rows.filter((r) => (selectable ? selectable(r) : true)).map(rowKey)
    : [];
  return (
    <div
      className={cn("transition-opacity", fetching && !loading && "opacity-70")}
      aria-busy={loading || fetching || undefined}
    >
      <Table aria-label={label} containerClassName={cn("bg-card", maxHeight)}>
        <TableHeader>
          <TableRow className="hover:bg-transparent">
            {selection ? (
              <TableHead className="w-10">
                <Checkbox
                  aria-label={t("adminCommon.table.selectAll")}
                  checked={selection.headerState(selectableKeys)}
                  disabled={selectableKeys.length === 0}
                  onCheckedChange={(on) =>
                    selection.toggleAll(selectableKeys, on)
                  }
                />
              </TableHead>
            ) : null}
            {columns.map((c) => (
              <TableHead
                key={c.id}
                className={cn(c.sticky && STICKY[c.sticky], c.headClassName)}
              >
                {c.header}
              </TableHead>
            ))}
          </TableRow>
        </TableHeader>
        <TableBody>
          {loading ? (
            Array.from({ length: skeletonRows }, (_, i) => (
              <TableRow key={`s${i}`} className="hover:bg-transparent">
                {Array.from({ length: span }, (_, j) => (
                  <TableCell key={j}>
                    <Skeleton className="h-4 w-full max-w-40" />
                  </TableCell>
                ))}
              </TableRow>
            ))
          ) : rows.length === 0 && error ? (
            <TableEmptyRow colSpan={span}>
              <div role="alert" className="flex flex-col items-center gap-2 py-2">
                <span className="flex items-center gap-2 text-destructive">
                  <AlertTriangle className="size-4" aria-hidden />
                  {t("adminCommon.table.loadFailed")}
                </span>
                {onRetry ? (
                  <Button variant="outline" size="sm" onClick={onRetry}>
                    {t("adminCommon.table.retry")}
                  </Button>
                ) : null}
              </div>
            </TableEmptyRow>
          ) : rows.length === 0 ? (
            <TableEmptyRow colSpan={span}>
              {empty ?? t("common.table.empty")}
            </TableEmptyRow>
          ) : (
            rows.map((row, index) => {
              const key = rowKey(row);
              const canSelect = selectable ? selectable(row) : true;
              const expanded = renderExpanded?.(row);
              return (
                <Fragment key={key}>
                  <TableRow
                    tone={rowTone?.(row)}
                    data-state={selection?.has(key) ? "selected" : undefined}
                    className={cn(onRowClick && "cursor-pointer focus-visible:outline-2 focus-visible:-outline-offset-2 focus-visible:outline-ring")}
                    onClick={onRowClick ? () => onRowClick(row) : undefined}
                    tabIndex={onRowClick ? 0 : undefined}
                    onKeyDown={
                      onRowClick
                        ? (event) => {
                            // Only the row itself opens; keys meant for controls inside it are left alone.
                            if (event.target === event.currentTarget && (event.key === "Enter" || event.key === " ")) {
                              event.preventDefault();
                              onRowClick(row);
                            }
                          }
                        : undefined
                    }
                  >
                    {selection ? (
                      <TableCell
                        className="w-10"
                        onClick={(event) => event.stopPropagation()}
                      >
                        <Checkbox
                          aria-label={rowLabel ? t("adminCommon.table.selectRowNamed", { name: rowLabel(row) }) : t("adminCommon.table.selectRow")}
                          checked={selection.has(key)}
                          disabled={!canSelect}
                          onCheckedChange={(on) => selection.toggle(key, on)}
                        />
                      </TableCell>
                    ) : null}
                    {columns.map((c) => (
                      <TableCell
                        key={c.id}
                        className={cn(
                          c.sticky && STICKY[c.sticky],
                          c.className,
                        )}
                      >
                        {c.cell(row, index)}
                      </TableCell>
                    ))}
                  </TableRow>
                  {expanded ? (
                    <TableRow className="bg-muted/20 hover:bg-muted/20">
                      <TableCell colSpan={span} className="p-3">
                        {expanded}
                      </TableCell>
                    </TableRow>
                  ) : null}
                </Fragment>
              );
            })
          )}
        </TableBody>
      </Table>
    </div>
  );
}

/** Stops a click on an interactive cell from also opening the row. */
export function CellActions({
  children,
  className,
}: {
  children: ReactNode;
  className?: string;
}) {
  return (
    <div
      className={cn("flex items-center gap-1", className)}
      onClick={(event) => event.stopPropagation()}
    >
      {children}
    </div>
  );
}
