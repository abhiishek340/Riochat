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
