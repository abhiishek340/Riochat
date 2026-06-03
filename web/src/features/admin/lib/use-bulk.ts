"use client";

import { useCallback, useRef, useState } from "react";
import { toast } from "@/components/ui/toast-store";
import { useT, type MessageKey } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { needsProgress, runBulk } from "./bulk";
import { useNotify } from "./use-notify";
import type { Selection } from "./use-selection";

export interface BulkRun<T> {
  targets: readonly T[];
  /** True when the item already has the requested value. */
  alreadyDone: (item: T) => boolean;
  /** The row as it should look while the request is in flight. */
  optimistic: (item: T) => T;
  request: (item: T) => Promise<T | void>;
  /** Toast title on full success; receives `{count}`. */
  successKey: MessageKey;
  chunkSize?: number;
  sequential?: boolean;
}

/**
 * Bulk edits of selected rows: items that already match are skipped, the rest
 * change at once and each goes out as its own request. Failed rows roll back
 * and stay selected; a total failure rolls everything back.
 */
export function useBulk<T, K extends string | number>(options: { getKey: (item: T) => K; setRows: (update: (rows: T[]) => T[]) => void; selection: Selection<K> }) {
  const { getKey, setRows, selection } = options;
  const t = useT();
  const message = useErrorMessage();
  const notify = useNotify();
  const [pending, setPending] = useState(false);
  const busy = useRef(false);

  const run = useCallback(
    async (job: BulkRun<T>): Promise<boolean> => {
      if (busy.current) return false;
      const targets = job.targets.filter((item) => !job.alreadyDone(item));
      if (targets.length === 0) {
        notify.info("adminCommon.toast.alreadyApplied");
        return false;
      }
      busy.current = true;
      setPending(true);
      const before = new Map(targets.map((item) => [getKey(item), item]));
      const keys = new Set(before.keys());
      setRows((rows) => rows.map((row) => (keys.has(getKey(row)) ? job.optimistic(row) : row)));
      const chunkSize = job.chunkSize ?? 100;
      const progressId = needsProgress(targets.length, chunkSize) ? "admin-bulk-progress" : null;
      try {
        const outcome = await runBulk(
          targets,
          async (item) => {
            const server = await job.request(item);
            if (server !== undefined && server !== null) {
              const row = server as T;
              setRows((rows) => rows.map((r) => (getKey(r) === getKey(item) ? row : r)));
            }
          },
          {
            chunkSize,
            sequential: job.sequential,
            onProgress: progressId ? (done, total) => toast.loading(t("adminCommon.toast.bulkProgress", { processed: done, total }), { id: progressId }) : undefined,
          },
        );
        if (progressId) toast.dismiss(progressId);
        const failed = new Set(outcome.failed.map((f) => getKey(f.item)));
        if (failed.size > 0) {
          setRows((rows) => rows.map((row) => (failed.has(getKey(row)) ? (before.get(getKey(row)) ?? row) : row)));
          selection.set(failed);
          toast.error(t("adminCommon.toast.bulkPartial"), {
            description: `${t("adminCommon.toast.bulkPartialBody", { succeeded: outcome.succeeded.length, failed: failed.size })} ${message(outcome.failed[0]?.error)}`,
          });
          return false;
        }
        notify.success(job.successKey, { count: targets.length });
        selection.clear();
        return true;
      } catch (error) {
        if (progressId) toast.dismiss(progressId);
        setRows((rows) => rows.map((row) => (keys.has(getKey(row)) ? (before.get(getKey(row)) ?? row) : row)));
        notify.error("adminCommon.toast.bulkFailed", error);
        return false;
      } finally {
        busy.current = false;
        setPending(false);
      }
    },
    [getKey, setRows, selection, notify, message, t],
  );

  return { run, pending };
}
