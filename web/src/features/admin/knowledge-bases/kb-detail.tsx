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

function AddFilesDialog({ open, kb, onOpenChange, onAdded }: { open: boolean; kb: KnowledgeBase; onOpenChange: (open: boolean) => void; onAdded: (count: number) => void }) {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [rows, setRows] = useState<KbFile[]>([]);
  const [page, setPage] = useState(1);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const selection = useSelection<string>();

  const load = useCallback(
    (signal?: AbortSignal) => {
      setLoading(true);
      return listAvailableFiles(kb.publicID, q, page, 10, signal).then(
        (result) => {
          setRows(result.results);
          setTotal(result.total);
          setLoading(false);
        },
        (error: unknown) => {
          if (isAbortError(error)) return;
          setLoading(false);
          notify.error("adminKb.toast.filesLoadFailed", error);
        },
      );
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [kb.publicID, q, page],
  );

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    void load(controller.signal);
    return () => controller.abort();
  }, [open, load]);

  useEffect(() => {
    if (open) {
      setQ("");
      setPage(1);
      selection.clear();
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const add = async () => {
    const ids = [...selection.selected];
    if (ids.length === 0) return;
    setBusy(true);
    try {
      await addKbFiles(kb.publicID, ids);
      notify.success("adminKb.toast.added", { count: ids.length });
      onAdded(ids.length);
      onOpenChange(false);
    } catch (error) {
      notify.error("adminKb.toast.addFailed", error);
    } finally {
      setBusy(false);
    }
  };

  const keys = rows.map((r) => r.fileID);
  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("adminKb.files.addTitle")}</DialogTitle>
          <DialogDescription>{t("adminKb.files.addBody")}</DialogDescription>
        </DialogHeader>
        <SearchBox
          value={q}
          onChange={(v) => {
            setQ(v);
            setPage(1);
          }}
          placeholder={t("adminKb.files.search")}
          className="sm:w-full"
        />
        <div className="max-h-72 overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="sticky top-0 bg-muted/90 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="w-10 px-3 py-2">
                  <Checkbox aria-label={t("adminCommon.table.selectAll")} checked={selection.headerState(keys)} disabled={keys.length === 0} onCheckedChange={(on) => selection.toggleAll(keys, on)} />
                </th>
                <th className="px-3 py-2">{t("adminKb.files.columns.name")}</th>
                <th className="px-3 py-2">{t("adminKb.files.columns.size")}</th>
              </tr>
            </thead>
            <tbody className={loading ? "opacity-60" : undefined}>
              {rows.map((r) => (
                <tr key={r.fileID} className="border-t">
                  <td className="px-3 py-2">
                    <Checkbox aria-label={t("adminCommon.table.selectRowNamed", { name: r.fileName })} checked={selection.has(r.fileID)} onCheckedChange={(on) => selection.toggle(r.fileID, on)} />
                  </td>
                  <td className="px-3 py-2">{r.fileName}</td>
                  <td className="whitespace-nowrap px-3 py-2 tabular-nums">{formatBytes(r.sizeBytes, locale)}</td>
                </tr>
              ))}
              {rows.length === 0 && !loading ? (
                <tr>
                  <td colSpan={3} className="h-20 text-center text-muted-foreground">
                    {t("adminKb.files.noneAvailable")}
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
        <Pagination page={page} pageSize={10} total={total} onPage={setPage} />
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={busy} disabled={selection.count === 0} onClick={add}>
            {t("adminKb.files.addSelected", { count: selection.count })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
