"use client";

import { useEffect, useRef, useState } from "react";
import { Trash2, Upload } from "lucide-react";
import {
  Button,
  ConfirmDialog,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  EmptyState,
  Input,
  Skeleton,
  Spinner,
  toast,
  Tooltip,
} from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { isAbortError } from "@/lib/api/errors";
import { retryNetwork, runPool } from "../shared/async";
import { uploadFile } from "../files/api";
import { formatBytes } from "../shared/format";
import { announceKbChanged } from "../shared/invalidation";
import { hasSession } from "../shared/session";
import { Checkbox } from "../shared/ui/checkbox";
import { FileIcon } from "../shared/ui/file-icon";
import { RetrievalBadge } from "../shared/ui/status-badge";
import type { KnowledgeBase } from "../shared/types";
import { addFilesToKb, deletePlatformFile, uploadPlatformFile, type KbMode } from "./api";
import { MAX_FILES_PER_CALL, nameMatches, toggleCapped, uniqueIDs } from "./model";
import { useAvailableFiles } from "./use-available-files";

const UPLOAD_CONCURRENCY = 4;

export interface KbAddDialogProps {
  mode: KbMode;
  kb: KnowledgeBase;
  open: boolean;
  onClose: () => void;
  /** Called after files were added: refresh the base's files and the list. */
  onAdded: () => void;
}

/** Pick existing files, or upload new ones, and add them to a knowledge base. */
export function KbAddDialog({ mode, kb, open, onClose, onAdded }: KbAddDialogProps) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [query, setQuery] = useState("");
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [adding, setAdding] = useState(false);
  const [uploading, setUploading] = useState(false);
  const [sourceToDelete, setSourceToDelete] = useState<{ fileID: string; fileName: string } | null>(null);
  const [deletingSource, setDeletingSource] = useState(false);
  const picker = useRef<HTMLInputElement>(null);
  const uploadCtrl = useRef<AbortController | null>(null);
  const available = useAvailableFiles(mode, kb.publicID, open, query);

  const busy = adding || uploading || deletingSource;
  const admin = mode === "admin";

  useEffect(() => {
    if (!open) {
      setQuery("");
      setSelected(new Set());
    }
  }, [open]);
  useEffect(
    () => () => {
      uploadCtrl.current?.abort();
    },
    [],
  );

  const visible = available.items.filter((f) => nameMatches(f.fileName, query));
  const atLimit = selected.size >= MAX_FILES_PER_CALL;

  const finish = () => {
    setSelected(new Set());
    setQuery("");
    onAdded();
    announceKbChanged(kb.publicID);
    onClose();
  };

  const add = async () => {
    if (selected.size > MAX_FILES_PER_CALL) {
      toast.error(t("kbWs.toasts.limit", { max: MAX_FILES_PER_CALL }));
      return;
    }
    if (selected.size === 0) return;
    setAdding(true);
    try {
      await addFilesToKb(mode, kb.publicID, [...selected]);
      finish();
      toast.success(t("kbWs.toasts.filesAdded"));
    } catch (error) {
      toast.error(t("kbWs.toasts.addFailed"), { description: errorMessage(error) });
    } finally {
      setAdding(false);
    }
  };

  const upload = async (files: File[]) => {
    if (files.length === 0) return;
    if (files.length > MAX_FILES_PER_CALL) {
      toast.error(t("kbWs.toasts.limit", { max: MAX_FILES_PER_CALL }));
      return;
    }
    if (!(await hasSession())) {
      toast.error(t("kbWs.toasts.sessionExpired"), { description: t("kbWs.toasts.signIn") });
      return;
    }
    uploadCtrl.current?.abort();
    const ctrl = new AbortController();
    uploadCtrl.current = ctrl;
    setUploading(true);
    try {
      const results = await runPool(
        files,
        UPLOAD_CONCURRENCY,
        (file) =>
          retryNetwork(async () => (admin ? (await uploadPlatformFile(file, ctrl.signal)).fileID : (await uploadFile(file, ctrl.signal)).file.fileID), {
            signal: ctrl.signal,
          }),
        ctrl.signal,
      );
      if (ctrl.signal.aborted) return;
      const ids = uniqueIDs(results.flatMap((r) => (r.ok ? [r.value] : [])));
      const failures = results.flatMap((r) => (!r.ok && !isAbortError(r.error) ? [r.error] : []));
      if (ids.length === 0) {
        if (failures.length > 0) toast.error(t("kbWs.toasts.uploadFailed"), { description: errorMessage(failures[0]) });
        return;
      }
      try {
        await addFilesToKb(mode, kb.publicID, ids);
      } catch (error) {
        toast.error(t("kbWs.toasts.uploadedNotAdded"), { description: errorMessage(error) });
        return;
      }
      finish();
      toast.success(t("kbWs.toasts.uploadedAdded", { count: ids.length }));
      if (failures.length > 0) {
        toast.error(t("kbWs.toasts.uploadPartial"), {
          description: t("kbWs.toasts.uploadPartialBody", { success: ids.length, failed: failures.length, reason: errorMessage(failures[0]) }),
        });
      }
    } finally {
      if (uploadCtrl.current === ctrl) setUploading(false);
    }
  };

  const confirmDeleteSource = async () => {
    if (!sourceToDelete || busy) return;
    setDeletingSource(true);
    try {
      await deletePlatformFile(sourceToDelete.fileID);
      available.removeLocal(sourceToDelete.fileID);
      setSelected((prev) => {
        const next = new Set(prev);
        next.delete(sourceToDelete.fileID);
        return next;
      });
      announceKbChanged();
      toast.success(t("kbWs.toasts.sourceDeleted"));
    } catch (error) {
      toast.error(t("kbWs.toasts.sourceDeleteFailed"), { description: errorMessage(error) });
      throw error;
    } finally {
      setDeletingSource(false);
    }
  };

  return (
    <>
      <Dialog open={open} onOpenChange={(next) => !next && !busy && onClose()}>
        <DialogContent className="flex max-h-[min(85dvh,44rem)] max-w-2xl flex-col gap-3">
          <DialogHeader>
            <DialogTitle>{t("kbWs.add.title")}</DialogTitle>
            <DialogDescription>{admin ? t("kbWs.add.adminBody", { name: kb.name }) : t("kbWs.add.userBody", { name: kb.name })}</DialogDescription>
          </DialogHeader>

          <div className="flex items-center gap-2">
            <Input
              value={query}
              disabled={busy}
              onChange={(e) => setQuery(e.target.value)}
              placeholder={admin ? t("kbWs.add.searchAdmin") : t("kbWs.add.searchUser")}
              aria-label={admin ? t("kbWs.add.searchAdmin") : t("kbWs.add.searchUser")}
            />
            <Button variant="outline" disabled={busy} loading={uploading} onClick={() => picker.current?.click()}>
              <Upload />
              {uploading ? t("kbWs.add.uploading") : t("kbWs.add.upload")}
            </Button>
            <input
              ref={picker}
              type="file"
              multiple
              hidden
              data-testid="kb-file-picker"
              onChange={(e) => {
                const files = Array.from(e.target.files ?? []);
                e.target.value = "";
                void upload(files);
              }}
            />
          </div>

          <div className="min-h-40 flex-1 overflow-y-auto rounded-lg border">
            {available.loading ? (
              <div className="grid gap-2 p-2" aria-busy role="status" aria-label={t("common.states.loading")}>
                {Array.from({ length: 4 }, (_, i) => (
                  <Skeleton key={i} className="h-11" />
                ))}
              </div>
            ) : available.failed ? (
              <EmptyState
                title={t("kbWs.add.loadFailed")}
                className="py-10"
                action={
                  <Button variant="outline" size="sm" onClick={available.retry}>
                    {t("common.actions.retry")}
                  </Button>
                }
              />
            ) : visible.length === 0 ? (
              <EmptyState title={admin ? t("kbWs.add.emptyAdmin") : t("kbWs.add.emptyUser")} className="py-10" />
            ) : (
              <ul aria-label={t("kbWs.add.title")}>
                {visible.map((file) => {
                  const checked = selected.has(file.fileID);
                  const blocked = !checked && atLimit;
                  const toggle = () => !busy && !blocked && setSelected((prev) => toggleCapped(prev, file.fileID, MAX_FILES_PER_CALL));
                  return (
                    // The row is a mouse convenience; the checkbox is the keyboard and
                    // assistive-technology control (Space natively, Enter added below).
                    <li
                      key={file.fileID}
                      data-selected={checked || undefined}
                      onClick={toggle}
                      className={cn(
                        "flex cursor-pointer items-center gap-3 border-b px-3 py-2 last:border-b-0 hover:bg-accent/50 focus-within:bg-accent/50",
                        (blocked || busy) && "cursor-not-allowed opacity-60",
                      )}
                    >
                      <Checkbox
                        checked={checked}
                        disabled={blocked || busy}
                        onCheckedChange={toggle}
                        onKeyDown={(e) => {
                          if (e.key === "Enter") {
                            e.preventDefault();
                            toggle();
                          }
                        }}
                        aria-label={t("kbWs.detail.rowCheckbox", { name: file.fileName })}
                      />
                      <FileIcon file={file} className="size-4 shrink-0 text-muted-foreground" />
                      <span className="min-w-0 flex-1">
                        <span className="block truncate text-sm">{file.fileName}</span>
                        <span className="block text-xs text-muted-foreground">{formatBytes(file.sizeBytes)}</span>
                      </span>
                      <RetrievalBadge file={file} />
                      {admin ? (
                        <Tooltip label={t("kbWs.add.deleteSource")}>
                          <Button
                            variant="ghost"
                            size="icon-xs"
                            aria-label={t("kbWs.add.deleteSourceNamed", { name: file.fileName })}
                            disabled={busy}
                            className="hover:text-destructive"
                            onClick={(e) => {
                              e.stopPropagation();
                              setSourceToDelete({ fileID: file.fileID, fileName: file.fileName });
                            }}
                          >
                            <Trash2 />
                          </Button>
                        </Tooltip>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
            )}
            {available.items.length < available.total && !available.loading ? (
              <div className="flex justify-center p-2">
                <Button variant="ghost" size="sm" disabled={available.loadingMore || busy} onClick={available.loadMore}>
                  {available.loadingMore ? <Spinner /> : null}
                  {t("kbWs.add.loadMore")}
                </Button>
              </div>
            ) : null}
          </div>

          <DialogFooter className="items-center sm:justify-between">
            <span className="text-sm text-muted-foreground" aria-live="polite">
              {t("kbWs.add.selected", { count: selected.size, max: MAX_FILES_PER_CALL })}
            </span>
            <div className="flex flex-col-reverse gap-2 sm:flex-row">
              <Button variant="outline" disabled={busy} onClick={onClose}>
                {t("common.actions.cancel")}
              </Button>
              <Button disabled={selected.size === 0 || busy} loading={adding} onClick={() => void add()}>
                {t("kbWs.add.add")}
              </Button>
            </div>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <ConfirmDialog
        open={sourceToDelete !== null}
        onOpenChange={(next) => !next && setSourceToDelete(null)}
        title={t("kbWs.add.deleteSourceTitle")}
        description={t("kbWs.add.deleteSourceBody", { name: sourceToDelete?.fileName ?? "" })}
        confirmLabel={t("kbWs.dialogs.confirm")}
        destructive
        onConfirm={confirmDeleteSource}
      />
    </>
  );
}
