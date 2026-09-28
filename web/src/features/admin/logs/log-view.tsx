"use client";

import { useState, type ReactNode } from "react";
import { Section } from "../components/page";
import { DataTable, type Column } from "../components/data-table";
import { Pagination } from "../components/pagination";
import { Sheet } from "../components/sheet";
import { Toolbar } from "../components/toolbar";
import type { PagedQuery } from "../lib/use-paged-query";
import { useSnapshot } from "../lib/use-snapshot";

export interface LogViewProps<T> {
  list: PagedQuery<T>;
  columns: Column<T>[];
  rowKey: (row: T) => number | string;
  filters: ReactNode;
  /** Sheet content for a row; the sheet opens when a row is clicked. */
  detail?: (row: T) => ReactNode;
  detailTitle?: (row: T) => ReactNode;
  label: string;
  empty: string;
  /** Rows shown instead of the server page (for client-side sorting). */
  rows?: readonly T[];
}

/** One log tab: filter bar, table, paging and a detail sheet. */
export function LogView<T>({ list, columns, rowKey, filters, detail, detailTitle, label, empty, rows }: LogViewProps<T>) {
  const [open, setOpen] = useState<T | null>(null);
  const shown = useSnapshot(open);
  return (
    <Section flush>
      <div className="space-y-3 p-4">
        <Toolbar>{filters}</Toolbar>
        <DataTable columns={columns} rows={rows ?? list.rows} rowKey={rowKey} loading={list.loading} error={list.error} onRetry={list.reload} fetching={list.fetching} onRowClick={detail ? setOpen : undefined} empty={empty} label={label} />
        <Pagination page={list.page} pageSize={list.pageSize} total={list.total} onPage={list.setPage} onPageSize={list.setPageSize} disabled={list.fetching} />
      </div>
      {detail ? (
        <Sheet open={open !== null} onOpenChange={(next) => !next && setOpen(null)} size="lg" title={shown && detailTitle ? detailTitle(shown) : label}>
          {shown ? detail(shown) : null}
        </Sheet>
      ) : null}
    </Section>
  );
}
