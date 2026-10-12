"use client";

import { Plus, Trash2 } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { isAbortError } from "@/lib/api/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import { Checkbox } from "../components/checkbox";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { Pagination } from "../components/pagination";
import { Sheet } from "../components/sheet";
import { SearchBox } from "../components/toolbar";
import { formatBytes } from "../lib/format";
import { createPoller } from "../lib/polling";
import { useNotify } from "../lib/use-notify";
import { useSelection } from "../lib/use-selection";
import { addKbFiles, kbSnapshot, listAvailableFiles, listKbFiles, removeKbFile, type KbFile, type KnowledgeBase } from "./api";
import { anyProcessing, applyStatuses, fileState } from "./rules";

const TONE = { ready: "success", processing: "primary", failed: "danger", waiting: "neutral" } as const;

export function FileStateBadge({ file }: { file: KbFile }) {
  const t = useT();
  const state = fileState(file);
  return <Badge tone={TONE[state]}>{t(`adminKb.fileState.${state}`)}</Badge>;
}

export function KbDetailSheet({ kb, onClose, onChanged }: { kb: KnowledgeBase; onClose: () => void; onChanged: (kb: KnowledgeBase) => void }) {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const [files, setFiles] = useState<KbFile[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [nonce, setNonce] = useState(0);
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<KbFile | null>(null);
  const filesRef = useRef(files);
  filesRef.current = files;
  const kbRef = useRef(kb);
  kbRef.current = kb;
  const onChangedRef = useRef(onChanged);
  onChangedRef.current = onChanged;

  useEffect(() => {
    const controller = new AbortController();
    setLoading(true);
    setFailed(false);
    listKbFiles(kb.publicID, page, pageSize, controller.signal).then(
      (result) => {
        setFiles(result.results);
        setTotal(result.total);
        setLoading(false);
      },
      (error: unknown) => {
        if (isAbortError(error)) return;
        setLoading(false);
        setFailed(true);
        notify.error("adminKb.toast.filesLoadFailed", error);
      },
    );
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [kb.publicID, page, pageSize, nonce]);

  // While files are still processing, refresh their state and the base's counters.
  const processing = anyProcessing(files);
  useEffect(() => {
    if (!processing) return;
    const poller = createPoller({
      intervalMs: 3000,
      maxFailures: 5,
      isHidden: () => document.visibilityState === "hidden",
      run: async () => {
        const ids = filesRef.current.filter((f) => f.processing && !f.processingReady).map((f) => f.fileID);
        if (ids.length === 0) return false;
        const snapshot = await kbSnapshot(kbRef.current.publicID, ids);
        setFiles((current) => applyStatuses(current, snapshot.statuses));
        if (snapshot.knowledgeBase) onChangedRef.current(snapshot.knowledgeBase);
        return snapshot.statuses.some((s) => s.processing && !s.processingReady);
      },
    });
    poller.start();
    return () => poller.stop();
  }, [processing]);

  const remove = async (file: KbFile) => {
    try {
      await removeKbFile(kb.publicID, file.fileID);
    } catch (error) {
      notify.error("adminKb.toast.removeFailed", error);
      throw error;
    }
    notify.success("adminKb.toast.removed");
    setNonce((n) => n + 1);
    onChangedRef.current({ ...kbRef.current, fileCount: Math.max(0, kbRef.current.fileCount - 1), readyFileCount: file.processingReady ? Math.max(0, kbRef.current.readyFileCount - 1) : kbRef.current.readyFileCount });
  };

  const columns: Column<KbFile>[] = [
    {
      id: "name",
      header: t("adminKb.files.columns.name"),
      cell: (f) => (
        <div className="min-w-0">
          <div className="truncate font-medium">{f.fileName}</div>
          <div className="truncate text-xs text-muted-foreground">{f.detectedMIME || f.mimeType}</div>
        </div>
      ),
    },
    { id: "size", header: t("adminKb.files.columns.size"), className: "whitespace-nowrap tabular-nums", cell: (f) => formatBytes(f.sizeBytes, locale) },
    {
      id: "state",
      header: t("adminKb.files.columns.state"),
      cell: (f) => (
        <div className="space-y-1">
          <FileStateBadge file={f} />
          {f.embedError ? <div className="max-w-56 truncate text-xs text-destructive">{f.embedError}</div> : null}
        </div>
      ),
    },
    { id: "chunks", header: t("adminKb.files.columns.chunks"), className: "tabular-nums", cell: (f) => f.chunkCount },
    {
      id: "actions",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (f) => (
        <CellActions>
          <Button variant="ghost" size="icon-sm" aria-label={`${t("adminKb.files.remove")}: ${f.fileName}`} onClick={() => setRemoving(f)}>
            <Trash2 className="text-destructive" />
          </Button>
        </CellActions>
      ),
    },
  ];

  return (
    <Sheet
      open
      onOpenChange={(open) => !open && onClose()}
      size="lg"
      title={kb.name}
      description={kb.description || t("adminKb.defaultDescription")}
      footer={
        <Button onClick={() => setAdding(true)}>
          <Plus />
          {t("adminKb.files.add")}
        </Button>
      }
    >
      <div className="space-y-3">
        <DataTable columns={columns} rows={files} rowKey={(f) => f.fileID} loading={loading} error={failed && files.length === 0} onRetry={() => setNonce((n) => n + 1)} empty={t("adminKb.files.empty")} label={t("adminKb.files.title")} />
        <Pagination page={page} pageSize={pageSize} total={total} onPage={setPage} onPageSize={setPageSize} />
      </div>
      <AddFilesDialog
        open={adding}
        kb={kb}
        onOpenChange={setAdding}
        onAdded={(count) => {
          setNonce((n) => n + 1);
          onChangedRef.current({ ...kbRef.current, fileCount: kbRef.current.fileCount + count, processingFileCount: kbRef.current.processingFileCount + count });
        }}
      />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={t("adminKb.files.removeTitle", { name: removing?.fileName ?? "" })}
        description={t("adminKb.files.removeBody")}
        confirmLabel={t("adminKb.files.remove")}
        destructive
        onConfirm={async () => {
          if (removing) await remove(removing);
        }}
      />
    </Sheet>
  );
}
