"use client";

import { useCallback, useState } from "react";
import { toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { runChunks } from "../shared/async";
import { announceKbChanged } from "../shared/invalidation";
import { hasSession } from "../shared/session";
import type { KnowledgeBase } from "../shared/types";
import { createKnowledgeBase, deleteKnowledgeBase, updateKnowledgeBase, type KbDraft, type KbMode } from "./api";
import type { KbList } from "./use-kb-list";

const DELETE_CHUNK = 10;

/** Create, edit, availability switch and (bulk) delete for knowledge bases. */
export function useKbActions(mode: KbMode, list: KbList, options: { onCreated?: () => void } = {}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [saving, setSaving] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [togglingId, setTogglingId] = useState<string | null>(null);
  const { load, select, setChecked } = list;
  const { onCreated } = options;

  /** Saves a draft; resolves true when the dialog may close. */
  const save = useCallback(
    async (editing: KnowledgeBase | null, draft: KbDraft): Promise<boolean> => {
      const clean = { name: draft.name.trim(), description: draft.description.trim() };
      if (clean.name === "") return false;
      setSaving(true);
      try {
        const saved = editing ? await updateKnowledgeBase(mode, editing.publicID, clean) : await createKnowledgeBase(mode, clean);
        select(saved.publicID);
        await load({ preferred: saved.publicID, silent: true });
        announceKbChanged(saved.publicID);
        if (!editing) onCreated?.();
        toast.success(t("kbWs.toasts.saved"));
        return true;
      } catch (error) {
        toast.error(t("kbWs.toasts.saveFailed"), { description: errorMessage(error) });
        return false;
      } finally {
        setSaving(false);
      }
    },
    [mode, load, select, onCreated, t, errorMessage],
  );

  /** Deletes the targets (one or many). Resolves true when the dialog may close. */
  const remove = useCallback(
    async (targets: readonly KnowledgeBase[], deleteFiles: boolean): Promise<boolean> => {
      if (targets.length === 0) return true;
      const single = targets.length === 1;
      setDeleting(true);
      const progressId = "kb-bulk-delete";
      try {
        if (!(await hasSession())) throw new Error("errors.auth.unauthorized");
        if (single) {
          const target = targets[0] as KnowledgeBase;
          await deleteKnowledgeBase(mode, target.publicID, deleteFiles);
          setChecked([]);
          await load({ silent: true });
          announceKbChanged(target.publicID);
          toast.success(t("kbWs.toasts.deleted"));
          return true;
        }
        const result = await runChunks(
          targets,
          DELETE_CHUNK,
          async (kb) => {
            await deleteKnowledgeBase(mode, kb.publicID, deleteFiles && kb.fileCount > 0);
          },
          (processed, total) => {
            if (targets.length > DELETE_CHUNK && processed < total) {
              toast.loading(t("kbWs.dialogs.progressTitle"), { id: progressId, description: t("kbWs.dialogs.progress", { processed, total }) });
            }
          },
        );
        toast.dismiss(progressId);
        setChecked([]);
        await load({ silent: true });
        if (result.success > 0) announceKbChanged();
        if (result.failed === 0) toast.success(t("kbWs.toasts.bulkDeleted", { count: result.success }));
        else
          toast.error(t("kbWs.toasts.bulkPartial"), {
            description: t("kbWs.toasts.bulkPartialBody", { success: result.success, failed: result.failed }),
          });
        return true;
      } catch (error) {
        toast.dismiss(progressId);
        toast.error(t("kbWs.toasts.deleteFailed"), { description: errorMessage(error) });
        return !single;
      } finally {
        setDeleting(false);
      }
    },
    [mode, load, setChecked, t, errorMessage],
  );

  const toggleEnabled = useCallback(
    async (kb: KnowledgeBase, enabled: boolean) => {
      if (mode !== "admin" || togglingId) return;
      setTogglingId(kb.publicID);
      try {
        await updateKnowledgeBase(mode, kb.publicID, { enabled });
        await load({ silent: true });
        announceKbChanged(kb.publicID);
        toast.success(enabled ? t("kbWs.toasts.enabled") : t("kbWs.toasts.disabledNow"));
      } catch (error) {
        toast.error(t("kbWs.toasts.toggleFailed"), { description: errorMessage(error) });
      } finally {
        setTogglingId(null);
      }
    },
    [mode, load, togglingId, t, errorMessage],
  );

  return { save, saving, remove, deleting, toggleEnabled, togglingId };
}

export type KbActions = ReturnType<typeof useKbActions>;
