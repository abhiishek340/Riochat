"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "@/components/ui";
import { isAbortError } from "@/lib/api/errors";
import { retryNetwork, runChunks, runPool } from "../shared/async";
import { markQueued } from "../shared/file-status";
import { hasSession } from "../shared/session";
import { planVectorize, summarizeEmbedding, MAX_VECTORIZE } from "../shared/vectorize";
import { summarizeUploads } from "../shared/upload-outcome";
import type { FileObject, StorageQuota } from "../shared/types";
import { deleteFile, patchFile, submitEmbeddings, uploadFile } from "./api";
import { mergeUploads, pruneChecked, selectionAfterDelete } from "./list-model";
import type { FileLibrary } from "./use-file-library";

const UPLOAD_CONCURRENCY = 4;
const DELETE_CHUNK = 10;

/** Every mutation of the files workspace: upload, delete, rename, retrieval opt-out and indexing. */
export function useFileActions(lib: FileLibrary) {
  const { setState, latest, reload, setUploading } = lib;
  const uploadCtrl = useRef<AbortController | null>(null);
  const vectorizing = useRef(false);
  const [deletingIds, setDeletingIds] = useState<ReadonlySet<string>>(new Set());
  const [bulkDeleting, setBulkDeleting] = useState(false);
  const [submittingIds, setSubmittingIds] = useState<ReadonlySet<string>>(new Set());
  const [vectorizeBusy, setVectorizeBusy] = useState(false);

  useEffect(
    () => () => {
      uploadCtrl.current?.abort();
    },
    [],
  );

  const replaceFile = useCallback(
    (fileID: string, next: (file: FileObject) => FileObject) =>
      setState((s) => ({ ...s, items: s.items.map((f) => (f.fileID === fileID ? next(f) : f)) })),
    [setState],
  );

  // --- upload ----------------------------------------------------------------
  const upload = useCallback(
    async (files: File[]) => {
      if (files.length === 0) return;
      const { t, errorMessage } = latest.current;
      if (!(await hasSession())) {
        toast.error(t("filesWs.toasts.sessionExpired"), { description: t("filesWs.toasts.signInToUpload") });
        return;
      }
      uploadCtrl.current?.abort();
      const ctrl = new AbortController();
      uploadCtrl.current = ctrl;
      setUploading(true);
      const results = await runPool(files, UPLOAD_CONCURRENCY, (file) => retryNetwork(() => uploadFile(file, ctrl.signal), { signal: ctrl.signal }), ctrl.signal);
      const current = uploadCtrl.current === ctrl;
      if (current) setUploading(false);
      if (ctrl.signal.aborted && !current) return;

      const done = results.flatMap((r) => (r.ok ? [r.value] : []));
      let selectID: string | null = null;
      if (done.length > 0) {
        const cur = latest.current;
        const merged = mergeUploads({ items: cur.state.items, total: cur.state.total, query: cur.debouncedQuery, filters: cur.filters, results: done });
        selectID = merged.selectID;
        setState((s) => {
          const again = mergeUploads({ items: s.items, total: s.total, query: cur.debouncedQuery, filters: cur.filters, results: done });
          return { ...s, items: again.items, total: again.total, quota: again.quota ?? s.quota, selectedId: again.selectID ?? s.selectedId };
        });
      }

      const outcome = summarizeUploads(results);
      switch (outcome.kind) {
        case "duplicate-only":
          toast.success(t("filesWs.toasts.duplicateOnly"));
          break;
        case "mixed":
          toast.success(t("filesWs.toasts.uploaded", { count: outcome.newCount }), { description: t("filesWs.toasts.duplicateNote") });
          break;
        case "clean":
          toast.success(t("filesWs.toasts.uploaded", { count: outcome.count }));
          break;
        case "all-failed":
          toast.error(t("filesWs.toasts.uploadFailed"), { description: errorMessage(outcome.error) });
          break;
        case "partial":
          toast.error(t("filesWs.toasts.uploadPartial"), {
            description: t("filesWs.toasts.uploadPartialBody", { success: outcome.success, failed: outcome.failed, reason: errorMessage(outcome.error) }),
          });
          break;
        default:
          break;
      }
      if (done.length > 0) void reload(selectID);
    },
    [latest, reload, setState, setUploading],
  );

  // --- delete ----------------------------------------------------------------
  const deleteOne = useCallback(
    async (fileID: string) => {
      const { t, errorMessage } = latest.current;
      if (!(await hasSession())) {
        toast.error(t("filesWs.toasts.sessionExpired"), { description: t("filesWs.toasts.signInToDelete") });
        return;
      }
      setDeletingIds((ids) => new Set(ids).add(fileID));
      try {
        const res = await deleteFile(fileID);
        setState((s) => {
          const items = s.items.filter((f) => f.fileID !== fileID);
          return {
            ...s,
            items,
            total: Math.max(0, s.total - 1),
            quota: res.quota ?? s.quota,
            selectedId: selectionAfterDelete(s.items, fileID, s.selectedId),
            checked: pruneChecked(s.checked, items),
          };
        });
        toast.success(t("filesWs.toasts.deleted"));
        void reload(latest.current.state.selectedId);
      } catch (error) {
        toast.error(t("filesWs.toasts.deleteFailed"), { description: errorMessage(error) });
      } finally {
        setDeletingIds((ids) => {
          const next = new Set(ids);
          next.delete(fileID);
          return next;
        });
      }
    },
    [latest, reload, setState],
  );

  const bulkDelete = useCallback(async () => {
    const { t, state } = latest.current;
    const ids = state.items.filter((f) => state.checked.has(f.fileID)).map((f) => f.fileID);
    if (ids.length === 0) return;
    if (!(await hasSession())) {
      toast.error(t("filesWs.toasts.sessionExpired"), { description: t("filesWs.toasts.signInToDelete") });
      return;
    }
    setBulkDeleting(true);
    const removed = new Set<string>();
    let quota: StorageQuota | null = null;
    const progressId = "files-bulk-delete";
    try {
      const result = await runChunks(
        ids,
        DELETE_CHUNK,
        async (id) => {
          const res = await deleteFile(id);
          removed.add(id);
          if (res.quota) quota = res.quota;
        },
        (processed, total) => {
          if (ids.length > DELETE_CHUNK && processed < total) {
            toast.loading(t("filesWs.dialogs.bulkProgressTitle"), { id: progressId, description: t("filesWs.dialogs.bulkProgress", { processed, total }) });
          }
        },
      );
      toast.dismiss(progressId);
      setState((s) => {
        const items = s.items.filter((f) => !removed.has(f.fileID));
        const selectedGone = s.selectedId !== null && removed.has(s.selectedId);
        return {
          ...s,
          items,
          total: Math.max(0, s.total - removed.size),
          quota: quota ?? s.quota,
          checked: new Set(),
          selectedId: selectedGone ? null : s.selectedId,
        };
      });
      void reload(latest.current.state.selectedId);
      if (result.failed === 0) toast.success(t("filesWs.toasts.bulkDeleted", { count: result.success }));
      else
        toast.error(t("filesWs.toasts.bulkPartial"), {
          description: t("filesWs.toasts.bulkPartialBody", { success: result.success, failed: result.failed }),
        });
    } finally {
      toast.dismiss(progressId);
      setBulkDeleting(false);
    }
  }, [latest, reload, setState]);

  // --- rename ----------------------------------------------------------------
  /** Returns true when the inline editor must stay open (empty name). */
  const rename = useCallback(
    async (fileID: string, rawName: string): Promise<boolean> => {
      const { t, errorMessage, state } = latest.current;
      const original = state.items.find((f) => f.fileID === fileID);
      if (!original) return false;
      const name = rawName.trim();
      if (name === "") {
        toast.error(t("filesWs.toasts.emptyName"));
        return true;
      }
      if (name === original.fileName) return false;
      if (!(await hasSession())) {
        toast.error(t("filesWs.toasts.sessionExpired"), { description: t("filesWs.toasts.signInToRename") });
        return false;
      }
      replaceFile(fileID, (f) => ({ ...f, fileName: name }));
      try {
        const updated = await patchFile(fileID, { fileName: name });
        replaceFile(fileID, () => updated);
        toast.success(t("filesWs.toasts.renamed"));
      } catch (error) {
        replaceFile(fileID, () => original);
        toast.error(t("filesWs.toasts.renameFailed"), { description: errorMessage(error) });
      }
      return false;
    },
    [latest, replaceFile],
  );

  // --- retrieval opt-out -----------------------------------------------------
  const toggleRag = useCallback(
    async (fileID: string) => {
      const { t, errorMessage, state } = latest.current;
      const original = state.items.find((f) => f.fileID === fileID);
      if (!original) return;
      const next = !original.ragOptOut;
      if (!(await hasSession())) {
        toast.error(t("filesWs.toasts.sessionExpired"), { description: t("filesWs.toasts.signInToContinue") });
        return;
      }
      replaceFile(fileID, (f) => ({ ...f, ragOptOut: next }));
      try {
        const updated = await patchFile(fileID, { ragOptOut: next });
        replaceFile(fileID, () => updated);
        toast.success(t(next ? "filesWs.toasts.ragNowOff" : "filesWs.toasts.ragNowOn"));
      } catch (error) {
        replaceFile(fileID, () => original);
        toast.error(t("filesWs.toasts.ragFailed"), { description: errorMessage(error) });
      }
    },
    [latest, replaceFile],
  );

  // --- indexing --------------------------------------------------------------
  const vectorize = useCallback(
    async (requested: string[]) => {
      if (vectorizing.current) return;
      const { t, errorMessage } = latest.current;
      const plan = planVectorize(requested);
      if (plan.ids.length === 0) return;
      if (plan.tooMany) {
        toast.error(t("filesWs.toasts.indexLimit", { max: MAX_VECTORIZE }));
        return;
      }
      if (!(await hasSession())) {
        toast.error(t("filesWs.toasts.sessionExpired"), { description: t("filesWs.toasts.signInToContinue") });
        return;
      }
      vectorizing.current = true;
      setVectorizeBusy(true);
      setSubmittingIds(new Set(plan.ids));
      try {
        const res = await submitEmbeddings(plan.ids);
        const submitted = new Set(res.submittedFileIDs ?? []);
        setState((s) => ({
          ...s,
          items: s.items.map((f) => (submitted.has(f.fileID) ? markQueued(f) : f)),
          checked: new Set([...s.checked].filter((id) => !submitted.has(id))),
        }));
        const outcome = summarizeEmbedding(res);
        if (outcome.kind === "partial") toast.warning(t("filesWs.toasts.indexPartial", { submitted: outcome.submitted, failed: outcome.failed }));
        else if (outcome.kind === "nothing") toast.info(t("filesWs.toasts.indexNothing"));
        else
          toast.success(t("filesWs.toasts.indexQueued", { count: outcome.count }), {
            description: outcome.skipped > 0 ? t("filesWs.toasts.indexSkipped", { count: outcome.skipped }) : undefined,
          });
      } catch (error) {
        if (!isAbortError(error)) toast.error(t("filesWs.toasts.indexFailed"), { description: errorMessage(error) });
      } finally {
        vectorizing.current = false;
        setVectorizeBusy(false);
        setSubmittingIds(new Set());
      }
    },
    [latest, setState],
  );

  return { upload, deleteOne, bulkDelete, rename, toggleRag, vectorize, deletingIds, bulkDeleting, submittingIds, vectorizeBusy };
}

export type FileActions = ReturnType<typeof useFileActions>;
