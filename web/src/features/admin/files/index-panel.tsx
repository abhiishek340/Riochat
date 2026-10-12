"use client";

import { RefreshCw, RotateCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { isAbortError } from "@/lib/api/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import { Checkbox } from "../components/checkbox";
import { SearchBox } from "../components/toolbar";
import { chunk } from "../lib/bulk";
import { formatBytes, formatDateTime, formatNumber } from "../lib/format";
import { createPoller } from "../lib/polling";
import { useNotify } from "../lib/use-notify";
import { useSelection } from "../lib/use-selection";
import { getEmbeddingStatus, listTasks, reindex, retryTasks, type EmbeddingTask } from "./api";
import { appendUnique, keepPolling, shouldPoll, TASK_BUCKETS, type EmbeddingStatus, type TaskBucket } from "./rules";

const POLL_MS = 3000;
const MAX_SILENT_FAILURES = 5;
const TASK_PAGE_SIZE = 50;

const COUNT_OF: Record<TaskBucket, keyof EmbeddingStatus> = {
  ready: "readyCount",
  pending: "pendingCount",
  failed: "failedCount",
  stale: "staleCount",
  empty: "emptyCount",
  unsupported: "unsupportedCount",
};

/** Index counters under the vector retrieval group, refreshed while work is running. */
export function IndexPanel({ resetKey }: { resetKey: unknown }) {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const [status, setStatus] = useState<EmbeddingStatus | null>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [rebuilding, setRebuilding] = useState(false);
  const [includeEmpty, setIncludeEmpty] = useState(false);
  const [bucket, setBucket] = useState<TaskBucket | null>(null);
  const controller = useRef<AbortController | null>(null);
  const poller = useRef<ReturnType<typeof createPoller> | null>(null);
  const lastStatus = useRef<EmbeddingStatus | null>(null);

  /** A newer load cancels whatever is still in flight. */
  const load = useCallback(async (silent: boolean): Promise<EmbeddingStatus | null> => {
    controller.current?.abort();
    const mine = new AbortController();
    controller.current = mine;
    if (!silent) setLoading(true);
    try {
      const next = await getEmbeddingStatus(mine.signal);
      if (controller.current !== mine) return null;
      lastStatus.current = next;
      setStatus(next);
      setFailed(false);
      setLoading(false);
      return next;
    } catch (error) {
      if (isAbortError(error) || controller.current !== mine) return null;
      if (!silent) {
        setFailed(true);
        setLoading(false);
        notify.error("adminFiles.index.loadFailed", error);
      }
      throw error;
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  useEffect(() => {
    poller.current = createPoller({
      intervalMs: POLL_MS,
      maxFailures: MAX_SILENT_FAILURES,
      isHidden: () => typeof document !== "undefined" && document.visibilityState === "hidden",
      run: async () => keepPolling(await load(true), lastStatus.current),
    });
    return () => {
      poller.current?.stop();
      controller.current?.abort();
    };
  }, [load]);

  useEffect(() => {
    void load(false).catch(() => undefined);
  }, [load, resetKey]);

  const polling = shouldPoll(status);
  useEffect(() => {
    if (polling) poller.current?.start();
    else poller.current?.stop();
  }, [polling]);

  const rebuild = async () => {
    setRebuilding(true);
    try {
      const count = await reindex(includeEmpty);
      if (count > 0) notify.success("adminFiles.index.submitted", { count });
      else notify.info("adminFiles.index.nothing");
      await load(false).catch(() => undefined);
    } catch (error) {
      notify.error("adminFiles.index.rebuildFailed", error);
    } finally {
      setRebuilding(false);
    }
  };

  return (
    <div className="mt-4 space-y-3 rounded-lg border bg-muted/30 p-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <div>
          <div className="text-sm font-medium">{t("adminFiles.index.title")}</div>
          <p className="text-[13px] text-muted-foreground">{t("adminFiles.index.description")}</p>
        </div>
        <Button variant="ghost" size="sm" onClick={() => void load(false).catch(() => undefined)} disabled={loading}>
          <RefreshCw />
          {t("adminFiles.index.refresh")}
        </Button>
      </div>
      {loading && !status ? (
        <Skeleton className="h-16 w-full" />
      ) : status ? (
        <>
          <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-6">
            {TASK_BUCKETS.map((b) => (
              <button key={b} type="button" onClick={() => setBucket(b)} className="rounded-lg border bg-card px-3 py-2 text-left transition-colors hover:bg-accent/50" aria-label={`${t(`adminFiles.index.buckets.${b}`)}: ${status[COUNT_OF[b]] as number}. ${t("adminFiles.index.view")}`}>
                <div className="text-xs text-muted-foreground">{t(`adminFiles.index.buckets.${b}`)}</div>
                <div className="text-lg font-semibold tabular-nums">{formatNumber(status[COUNT_OF[b]] as number, locale)}</div>
              </button>
            ))}
          </div>
          <div className="flex flex-wrap items-center gap-2 text-sm">
            {status.reindexRunning ? <Badge tone="primary">{t("adminFiles.index.running")}</Badge> : null}
            {status.stalledCount > 0 ? <Badge tone="warning">{t("adminFiles.index.stalled", { count: status.stalledCount })}</Badge> : null}
            {status.needsReindex ? <span className="text-warning-foreground dark:text-warning">{t("adminFiles.index.needsRebuild")}</span> : null}
          </div>
          <div className="flex flex-wrap items-center gap-3">
            <Button size="sm" variant={status.needsReindex ? "default" : "outline"} loading={rebuilding} disabled={status.reindexRunning} onClick={rebuild}>
              <RotateCw />
              {t("adminFiles.index.rebuild")}
            </Button>
            <label className="flex items-center gap-2 text-[13px]">
              <Checkbox checked={includeEmpty} onCheckedChange={setIncludeEmpty} />
              {t("adminFiles.index.rebuildEmpty")}
            </label>
          </div>
        </>
      ) : failed ? (
        <p className="text-sm text-destructive">{t("adminFiles.index.loadFailed")}</p>
      ) : null}
      <TasksDialog bucket={bucket} onClose={() => setBucket(null)} onChanged={() => void load(false).catch(() => undefined)} />
    </div>
  );
}

function TasksDialog({ bucket, onClose, onChanged }: { bucket: TaskBucket | null; onClose: () => void; onChanged: () => void }) {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [rows, setRows] = useState<EmbeddingTask[]>([]);
  const [page, setPage] = useState(1);
  const [done, setDone] = useState(false);
  const [loading, setLoading] = useState(false);
  const [retrying, setRetrying] = useState(false);
  const [nonce, setNonce] = useState(0);
  const selection = useSelection<string>();
  const request = useRef(0);
  // The follow-up page request is cancelled when the bucket, the search or the dialog changes.
  const moreRequest = useRef<AbortController | null>(null);
  const sentinel = useRef<HTMLDivElement>(null);

  // A new bucket, search or refresh starts again from page one.
  useEffect(() => {
    if (!bucket) return;
    const id = ++request.current;
    const controller = new AbortController();
    setRows([]);
    setPage(1);
    setDone(false);
    selection.clear();
    setLoading(true);
    listTasks({ bucket, page: 1, pageSize: TASK_PAGE_SIZE, query: q }, controller.signal).then(
      (result) => {
        if (id !== request.current) return;
        setRows(result.results);
        setDone(result.results.length < TASK_PAGE_SIZE || result.results.length >= result.total);
        setLoading(false);
      },
      (error: unknown) => {
        if (isAbortError(error) || id !== request.current) return;
        setLoading(false);
        setDone(true);
        notify.error("adminFiles.tasks.loadFailed", error);
      },
    );
    return () => {
      controller.abort();
      moreRequest.current?.abort();
      // Whatever is still on its way no longer belongs to the screen.
      request.current += 1;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bucket, q, nonce]);

  const loadMore = useCallback(async () => {
    if (!bucket || loading || done) return;
    const id = request.current;
    const next = page + 1;
    setLoading(true);
    const controller = new AbortController();
    moreRequest.current = controller;
    try {
      const result = await listTasks({ bucket, page: next, pageSize: TASK_PAGE_SIZE, query: q }, controller.signal);
      if (id !== request.current) return;
      setRows((current) => {
        const merged = appendUnique(current, result.results, (x) => x.fileID);
        // A page that adds nothing new ends the paging.
        if (!merged.added || merged.items.length >= result.total) setDone(true);
        return merged.items;
      });
      setPage(next);
    } catch (error) {
      if (!isAbortError(error)) {
        setDone(true);
        notify.error("adminFiles.tasks.loadFailed", error);
      }
    } finally {
      if (id === request.current) setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [bucket, loading, done, page, q]);

  useEffect(() => {
    const node = sentinel.current;
    if (!node || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && void loadMore());
    observer.observe(node);
    return () => observer.disconnect();
  }, [loadMore, rows.length]);

  const retryable = useMemo(() => rows.filter((r) => r.retryable).map((r) => r.fileID), [rows]);

  const retry = async () => {
    const ids = [...selection.selected];
    if (ids.length === 0) return;
    setRetrying(true);
    try {
      let submitted = 0;
      let skipped = 0;
      for (const group of chunk(ids, 100)) {
        const result = await retryTasks(group);
        submitted += result.submittedFileIDs.length;
        skipped += result.skipped.length;
      }
      if (submitted === 0) notify.warning("adminFiles.tasks.nothingRetried");
      else if (skipped > 0) notify.success("adminFiles.tasks.retried", { count: submitted }, t("adminFiles.tasks.retriedSkipped", { skipped }));
      else notify.success("adminFiles.tasks.retried", { count: submitted });
      selection.clear();
      setNonce((n) => n + 1);
      onChanged();
    } catch (error) {
      notify.error("adminFiles.tasks.retryFailed", error);
    } finally {
      setRetrying(false);
    }
  };

  return (
    <Dialog open={bucket !== null} onOpenChange={(open) => !open && !retrying && onClose()}>
      <DialogContent className="max-w-4xl">
        <DialogHeader>
          <DialogTitle>{t("adminFiles.tasks.title", { bucket: bucket ? t(`adminFiles.index.buckets.${bucket}`) : "" })}</DialogTitle>
        </DialogHeader>
        <div className="flex flex-wrap items-center gap-2">
          <SearchBox value={q} onChange={setQ} placeholder={t("adminFiles.tasks.search")} />
          <Button size="sm" className="ml-auto" disabled={selection.count === 0} loading={retrying} onClick={retry}>
            {t("adminFiles.tasks.retry")}
            {selection.count > 0 ? ` (${selection.count})` : ""}
          </Button>
        </div>
        <div className="max-h-[55vh] overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="sticky top-0 bg-muted/90 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="w-10 px-3 py-2">
                  <Checkbox
                    aria-label={t("adminCommon.table.selectAll")}
                    checked={selection.headerState(retryable)}
                    disabled={retryable.length === 0}
                    onCheckedChange={(on) => selection.toggleAll(retryable, on)}
                  />
                </th>
                <th className="px-3 py-2">{t("adminFiles.tasks.columns.file")}</th>
                <th className="px-3 py-2">{t("adminFiles.tasks.columns.owner")}</th>
                <th className="px-3 py-2">{t("adminFiles.tasks.columns.size")}</th>
                <th className="px-3 py-2">{t("adminFiles.tasks.columns.updated")}</th>
                <th className="px-3 py-2">{t("adminFiles.tasks.columns.note")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.fileID} className="border-t">
                  <td className="px-3 py-2">
                    <Checkbox aria-label={t("adminCommon.table.selectRowNamed", { name: r.fileName })} checked={selection.has(r.fileID)} disabled={!r.retryable} onCheckedChange={(on) => selection.toggle(r.fileID, on)} />
                  </td>
                  <td className="max-w-[16rem] px-3 py-2">
                    <div className="truncate font-medium">{r.fileName}</div>
                    <div className="truncate text-xs text-muted-foreground">{r.mimeType}</div>
                  </td>
                  <td className="px-3 py-2">{r.userLabel || r.userID}</td>
                  <td className="whitespace-nowrap px-3 py-2 tabular-nums">{formatBytes(r.sizeBytes, locale)}</td>
                  <td className="whitespace-nowrap px-3 py-2 text-muted-foreground">{formatDateTime(r.updatedAt, locale)}</td>
                  <td className="px-3 py-2 text-[13px]">
                    {r.stalled ? <Badge tone="warning">{t("adminFiles.tasks.stalled")}</Badge> : null}
                    {r.embedError ? <div className="text-destructive">{r.embedError}</div> : null}
                    {!r.retryable && r.retryBlockedReason ? <div className="text-muted-foreground">{t("adminFiles.tasks.cannotRetry")}: {r.retryBlockedReason}</div> : null}
                  </td>
                </tr>
              ))}
              {rows.length === 0 && !loading ? (
                <tr>
                  <td colSpan={6} className="h-24 text-center text-muted-foreground">
                    {t("adminFiles.tasks.empty")}
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
          <div ref={sentinel} className="flex h-10 items-center justify-center">
            {loading ? <Skeleton className="h-4 w-24" /> : !done ? (
              <Button variant="ghost" size="sm" onClick={() => void loadMore()}>
                {t("adminFiles.tasks.loadMore")}
              </Button>
            ) : null}
          </div>
        </div>
      </DialogContent>
    </Dialog>
  );
}
