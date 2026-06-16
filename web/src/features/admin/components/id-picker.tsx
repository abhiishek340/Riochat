"use client";

import { useEffect, useMemo, useState, type ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { toast } from "@/components/ui/toast-store";
import { toggleMany } from "../lib/selection";
import type { PageResult } from "../lib/paging";
import { Checkbox } from "./checkbox";
import { Pagination } from "./pagination";
import { SearchBox, Toolbar } from "./toolbar";

export interface IdPickerProps<T, F> {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  /** The ids chosen so far. */
  value: readonly number[];
  onApply: (ids: number[]) => void;
  filters: F;
  onFilters: (filters: F) => void;
  /** Extra filter controls shown beside the search box. */
  filterBar?: ReactNode;
  fetchPage: (args: { page: number; pageSize: number; q: string; filters: F; signal: AbortSignal }) => Promise<PageResult<T>>;
  /** Ids of every row matching the filters (all pages). */
  fetchAllIds: (args: { q: string; filters: F; signal: AbortSignal }) => Promise<number[]>;
  rowId: (row: T) => number;
  renderRow: (row: T) => ReactNode;
  searchPlaceholder?: string;
}

/**
 * Pick rows by id across pages: the page shows checkboxes, "select page" and
 * "select all results" add to the choice, "clear" empties it, and nothing
 * leaves the dialog until it is applied.
 */
export function IdPicker<T, F>({ open, onOpenChange, title, description, value, onApply, filters, filterBar, fetchPage, fetchAllIds, rowId, renderRow, searchPlaceholder }: IdPickerProps<T, F>) {
  const t = useT();
  const message = useErrorMessage();
  const [q, setQ] = useState("");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const [rows, setRows] = useState<T[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [draft, setDraft] = useState<Set<number>>(new Set(value));
  const [busyAll, setBusyAll] = useState(false);
  const filtersKey = JSON.stringify(filters);

  useEffect(() => {
    if (open) {
      setDraft(new Set(value));
      setQ("");
      setPage(1);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  useEffect(() => {
    setPage(1);
  }, [q, filtersKey, pageSize]);

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    setLoading(true);
    fetchPage({ page, pageSize, q, filters, signal: controller.signal }).then(
      (result) => {
        setRows(result.results);
        setTotal(result.total);
        setLoading(false);
      },
      (error: unknown) => {
        if (isAbortError(error)) return;
        setLoading(false);
        toast.error(t("adminCommon.toast.loadFailed"), { description: message(error) });
      },
    );
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, page, pageSize, q, filtersKey]);

  const pageIds = useMemo(() => rows.map(rowId), [rows, rowId]);
  const allOnPage = pageIds.length > 0 && pageIds.every((id) => draft.has(id));
  const someOnPage = pageIds.some((id) => draft.has(id));

  const selectAllResults = async () => {
    setBusyAll(true);
    try {
      const ids = await fetchAllIds({ q, filters, signal: new AbortController().signal });
      setDraft((d) => toggleMany(d, ids, true));
    } catch (error) {
      toast.error(t("adminCommon.toast.loadFailed"), { description: message(error) });
    } finally {
      setBusyAll(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description ? <DialogDescription>{description}</DialogDescription> : null}
        </DialogHeader>
        <Toolbar>
          <SearchBox value={q} onChange={setQ} placeholder={searchPlaceholder} />
          {filterBar}
        </Toolbar>
        <div className="flex flex-wrap items-center gap-2 text-sm">
          <label className="flex items-center gap-2">
            <Checkbox
              aria-label={t("adminGroups.picker.selectPage")}
              checked={allOnPage ? true : someOnPage ? "indeterminate" : false}
              disabled={pageIds.length === 0}
              onCheckedChange={(on) => setDraft((d) => toggleMany(d, pageIds, on))}
            />
            {t("adminGroups.picker.selectPage")}
          </label>
          <Button size="sm" variant="outline" loading={busyAll} onClick={selectAllResults}>
            {t("adminGroups.picker.selectAll", { count: total })}
          </Button>
          <Button size="sm" variant="ghost" onClick={() => setDraft(new Set())} disabled={draft.size === 0}>
            {t("adminGroups.picker.clear")}
          </Button>
          <span className="ml-auto text-muted-foreground">{t("adminGroups.picker.chosen", { count: draft.size })}</span>
        </div>
        <div className="max-h-[45vh] overflow-auto rounded-lg border">
          {loading && rows.length === 0 ? (
            <div className="space-y-2 p-3">
              {Array.from({ length: 5 }, (_, i) => (
                <Skeleton key={i} className="h-8 w-full" />
              ))}
            </div>
          ) : rows.length === 0 ? (
            <p className="p-6 text-center text-sm text-muted-foreground">{t("adminCommon.table.noResults")}</p>
          ) : (
            <ul className={loading ? "opacity-60" : undefined}>
              {rows.map((row) => {
                const id = rowId(row);
                return (
                  <li key={id} className="border-b last:border-0">
                    <label className="flex cursor-pointer items-center gap-3 px-3 py-2 hover:bg-muted/50">
                      <Checkbox
                        checked={draft.has(id)}
                        onCheckedChange={(on) =>
                          setDraft((d) => {
                            const next = new Set(d);
                            if (on) next.add(id);
                            else next.delete(id);
                            return next;
                          })
                        }
                      />
                      <div className="min-w-0 flex-1">{renderRow(row)}</div>
                    </label>
                  </li>
                );
              })}
            </ul>
          )}
        </div>
        <Pagination page={page} pageSize={pageSize} total={total} onPage={setPage} onPageSize={setPageSize} disabled={loading} />
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button
            onClick={() => {
              onApply([...draft]);
              onOpenChange(false);
            }}
          >
            {t("adminGroups.picker.apply", { count: draft.size })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
