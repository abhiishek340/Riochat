"use client";

import { Eye, Trash2, Upload } from "lucide-react";
import { useCallback, useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast-store";
import { isAbortError } from "@/lib/api/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { Checkbox } from "../components/checkbox";
import { SearchBox } from "../components/toolbar";
import { runBulk } from "../lib/bulk";
import { formatBytes, formatDateTime } from "../lib/format";
import { useNotify } from "../lib/use-notify";
import { useSelection } from "../lib/use-selection";
import { deletePoolFile, poolFileContent, poolFiles, submitEmbeddings, uploadPoolFile, type KbFile } from "./api";
import { FileStateBadge } from "./kb-detail";
import { batchTooLarge, DELETE_CHUNK, mergeFilePages, MAX_UPLOAD_BATCH, summarizeUploads } from "./rules";

const PAGE_SIZE = 50;

/** Platform-owned files: upload, preview, vectorise and delete. */
export function PoolDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const { locale } = useI18n();
  const message = useErrorMessage();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [rows, setRows] = useState<KbFile[]>([]);
  const [page, setPage] = useState(1);
  const [done, setDone] = useState(false);
  const [loading, setLoading] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [removing, setRemoving] = useState<KbFile[] | null>(null);
  const [preview, setPreview] = useState<{ file: KbFile; text: string | null; url: string | null; failed: boolean } | null>(null);
  const [nonce, setNonce] = useState(0);
  const selection = useSelection<string>();
  const version = useRef(0);
  // The follow-up page request is cancelled with the dialog or by any new load.
  const moreRequest = useRef<AbortController | null>(null);
  const input = useRef<HTMLInputElement>(null);

  // Opening, searching or refreshing starts a new versioned load; closing cancels it.
  useEffect(() => {
    if (!open) return;
    const mine = ++version.current;
    const controller = new AbortController();
    setRows([]);
    setPage(1);
    setDone(false);
    setLoading(true);
    selection.clear();
    poolFiles(q, 1, PAGE_SIZE, controller.signal).then(
      (result) => {
        if (mine !== version.current) return;
        setRows(result.results);
        setDone(result.results.length < PAGE_SIZE || result.results.length >= result.total);
        setLoading(false);
      },
      (error: unknown) => {
        if (isAbortError(error) || mine !== version.current) return;
        setLoading(false);
        setDone(true);
        notify.error("adminKb.toast.filesLoadFailed", error);
      },
    );
    return () => {
      controller.abort();
      moreRequest.current?.abort();
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, q, nonce]);

  const loadMore = useCallback(async () => {
    if (loading || done) return;
    const mine = version.current;
    const next = page + 1;
    setLoading(true);
    const controller = new AbortController();
    moreRequest.current = controller;
    try {
      const result = await poolFiles(q, next, PAGE_SIZE, controller.signal);
      if (mine !== version.current) return;
      setRows((current) => {
        const merged = mergeFilePages(current, result.results);
        if (merged.added === 0 || merged.items.length >= result.total) setDone(true);
        return merged.items;
      });
      setPage(next);
    } catch (error) {
      if (!isAbortError(error)) {
        setDone(true);
        notify.error("adminKb.toast.filesLoadFailed", error);
      }
    } finally {
      if (mine === version.current) setLoading(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loading, done, page, q]);

  useEffect(() => {
    if (!open) {
      setPreview((p) => {
        if (p?.url) URL.revokeObjectURL(p.url);
        return null;
      });
    }
  }, [open]);

  const upload = async (list: FileList | null) => {
    const files = list ? [...list] : [];
    if (input.current) input.current.value = "";
    if (files.length === 0) return;
    if (batchTooLarge(files.length)) {
      toast.error(t("adminKb.pool.tooMany", { max: MAX_UPLOAD_BATCH }));
      return;
    }
    setUploading(true);
    try {
      const outcome = summarizeUploads(await Promise.allSettled(files.map((f) => uploadPoolFile(f))));
      if (outcome.failed === 0) notify.success("adminKb.pool.uploaded", { count: outcome.ok });
      else toast.error(t("adminKb.pool.uploadPartial", { ok: outcome.ok, failed: outcome.failed }), { description: message(outcome.firstError) });
    } finally {
      setUploading(false);
      setNonce((n) => n + 1);
    }
  };

  const confirmDelete = async () => {
    if (!removing) return;
    const outcome = await runBulk(removing, (f) => deletePoolFile(f.fileID), { chunkSize: DELETE_CHUNK, sequential: true });
    if (outcome.failed.length > 0) toast.error(t("adminKb.pool.deletePartial", { ok: outcome.succeeded.length, failed: outcome.failed.length }), { description: message(outcome.failed[0]?.error) });
    else notify.success("adminKb.pool.deleted", { count: outcome.succeeded.length });
    selection.clear();
    setNonce((n) => n + 1);
  };

  const vectorise = async () => {
    const ids = [...selection.selected];
    if (ids.length === 0) return;
    try {
      const result = await submitEmbeddings(ids);
      if (result.submittedFileIDs.length === 0) notify.warning("adminKb.pool.vectoriseNone");
      else notify.success("adminKb.pool.vectorised", { count: result.submittedFileIDs.length });
      selection.clear();
      setNonce((n) => n + 1);
    } catch (error) {
      notify.error("adminKb.pool.vectoriseFailed", error);
    }
  };

  const openPreview = async (file: KbFile) => {
    setPreview({ file, text: null, url: null, failed: false });
    try {
      const blob = await poolFileContent(file.fileID);
      if (blob.type.startsWith("image/") || blob.type === "application/pdf") setPreview({ file, text: null, url: URL.createObjectURL(blob), failed: false });
      else setPreview({ file, text: (await blob.text()).slice(0, 20_000), url: null, failed: false });
    } catch {
      setPreview({ file, text: null, url: null, failed: true });
    }
  };

  const keys = rows.map((r) => r.fileID);
  return (
    <Dialog open={open} onOpenChange={(next) => !uploading && onOpenChange(next)}>
      <DialogContent className="max-w-4xl">
        <DialogHeader>
          <DialogTitle>{t("adminKb.pool.title")}</DialogTitle>
          <DialogDescription>{t("adminKb.pool.description")}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap items-center gap-2">
          <SearchBox value={q} onChange={setQ} placeholder={t("adminKb.files.search")} delay={200} />
          <div className="ml-auto flex flex-wrap items-center gap-2">
            {selection.count > 0 ? (
              <>
                <Button size="sm" variant="outline" onClick={vectorise}>
                  {t("adminKb.pool.vectorise", { count: selection.count })}
                </Button>
                <Button size="sm" variant="destructive" onClick={() => setRemoving(rows.filter((r) => selection.has(r.fileID)))}>
                  {t("adminKb.pool.deleteSelected", { count: selection.count })}
                </Button>
              </>
            ) : null}
            <input ref={input} type="file" multiple hidden onChange={(e) => void upload(e.target.files)} />
            <Button size="sm" loading={uploading} onClick={() => input.current?.click()}>
              <Upload />
              {t("adminKb.pool.upload")}
            </Button>
          </div>
        </div>
        <div className="max-h-[50vh] overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="sticky top-0 bg-muted/90 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="w-10 px-3 py-2">
                  <Checkbox aria-label={t("adminCommon.table.selectAll")} checked={selection.headerState(keys)} disabled={keys.length === 0} onCheckedChange={(on) => selection.toggleAll(keys, on)} />
                </th>
                <th className="px-3 py-2">{t("adminKb.files.columns.name")}</th>
                <th className="px-3 py-2">{t("adminKb.files.columns.size")}</th>
                <th className="px-3 py-2">{t("adminKb.files.columns.state")}</th>
                <th className="px-3 py-2">{t("adminKb.files.columns.updated")}</th>
                <th className="px-3 py-2" />
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.fileID} className="border-t">
                  <td className="px-3 py-2">
                    <Checkbox aria-label={t("adminCommon.table.selectRowNamed", { name: r.fileName })} checked={selection.has(r.fileID)} onCheckedChange={(on) => selection.toggle(r.fileID, on)} />
                  </td>
                  <td className="max-w-[16rem] px-3 py-2">
                    <div className="truncate font-medium">{r.fileName}</div>
                    <div className="truncate text-xs text-muted-foreground">{r.detectedMIME || r.mimeType}</div>
                  </td>
                  <td className="whitespace-nowrap px-3 py-2 tabular-nums">{formatBytes(r.sizeBytes, locale)}</td>
                  <td className="px-3 py-2">
                    <FileStateBadge file={r} />
                  </td>
                  <td className="whitespace-nowrap px-3 py-2 text-muted-foreground">{formatDateTime(r.updatedAt, locale)}</td>
                  <td className="whitespace-nowrap px-3 py-2 text-right">
                    <Button variant="ghost" size="icon-sm" aria-label={`${t("adminKb.pool.preview")}: ${r.fileName}`} onClick={() => void openPreview(r)}>
                      <Eye />
                    </Button>
                    <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.delete")}: ${r.fileName}`} onClick={() => setRemoving([r])}>
                      <Trash2 className="text-destructive" />
                    </Button>
                  </td>
                </tr>
              ))}
              {rows.length === 0 && !loading ? (
                <tr>
                  <td colSpan={6} className="h-24 text-center text-muted-foreground">
                    {t("adminKb.pool.empty")}
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
          <div className="flex h-10 items-center justify-center">
            {loading ? <Skeleton className="h-4 w-24" /> : !done ? (
              <Button variant="ghost" size="sm" onClick={() => void loadMore()}>
                {t("adminKb.pool.loadMore")}
              </Button>
            ) : null}
          </div>
        </div>

        <ConfirmDialog
          open={removing !== null}
          onOpenChange={(next) => !next && setRemoving(null)}
          title={t("adminKb.pool.deleteTitle", { count: removing?.length ?? 0 })}
          description={t("adminKb.pool.deleteBody")}
          confirmLabel={t("common.actions.delete")}
          destructive
          onConfirm={confirmDelete}
        />
        <Dialog
          open={preview !== null}
          onOpenChange={(next) => {
            if (!next) {
              if (preview?.url) URL.revokeObjectURL(preview.url);
              setPreview(null);
            }
          }}
        >
          <DialogContent className="max-w-3xl">
            <DialogHeader>
              <DialogTitle>{preview?.file.fileName ?? ""}</DialogTitle>
            </DialogHeader>
            {preview?.failed ? <p className="text-sm text-destructive">{t("adminKb.pool.previewFailed")}</p> : null}
            {preview && !preview.failed && preview.text === null && preview.url === null ? <Skeleton className="h-40 w-full" /> : null}
            {preview?.text !== null && preview?.text !== undefined ? <pre className="max-h-[60vh] overflow-auto whitespace-pre-wrap rounded-lg border bg-muted/40 p-3 font-mono text-[12.5px]">{preview.text}</pre> : null}
            {preview?.url && preview.file.mimeType === "application/pdf" ? <iframe title={preview.file.fileName} src={preview.url} sandbox="" className="h-[60vh] w-full rounded-lg border" /> : null}
            {preview?.url && preview.file.mimeType !== "application/pdf" ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={preview.url} alt={preview.file.fileName} className="max-h-[60vh] w-full object-contain" />
            ) : null}
          </DialogContent>
        </Dialog>
      </DialogContent>
    </Dialog>
  );
}
