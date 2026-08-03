"use client";

import { useCallback } from "react";
import { useRouter } from "next/navigation";
import { toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import {
  exportConversation,
  regenerateTitle,
  renameConversation,
  setConversationProject,
  setStar,
  updateLabels,
} from "../api";
import { downloadText, timestampForFile } from "../lib/clipboard";
import { safeFileName } from "../lib/format";
import type { ConversationDTO } from "../types";
import { chatEngine } from "../state/engine-instance";
import { deleteInList, patchConversation, upsertConversation } from "../state/conversation-store";

/** Actions on one conversation with their toasts. Each resolves to success. */
export function useConversationActions(onDeleted?: (id: string) => void) {
  const t = useT();
  const router = useRouter();
  const errorMessage = useErrorMessage();

  const apply = useCallback((updated: ConversationDTO) => {
    upsertConversation(updated);
  }, []);

  const toggleStar = useCallback(
    async (c: ConversationDTO): Promise<boolean> => {
      const next = !c.isStarred;
      patchConversation(c.publicID, { isStarred: next });
      try {
        apply(await setStar(c.publicID, next));
        return true;
      } catch (error) {
        patchConversation(c.publicID, { isStarred: c.isStarred });
        toast.error(errorMessage(error));
        return false;
      }
    },
    [apply, errorMessage],
  );

  const rename = useCallback(
    async (c: ConversationDTO, title: string): Promise<boolean> => {
      try {
        apply(await renameConversation(c.publicID, title.trim()));
        return true;
      } catch (error) {
        toast.error(t("chatNav.rename.failed"), { description: errorMessage(error) });
        return false;
      }
    },
    [apply, errorMessage, t],
  );

  const autoRename = useCallback(
    async (c: ConversationDTO): Promise<boolean> => {
      try {
        apply(await regenerateTitle(c.publicID));
        return true;
      } catch {
        toast.error(t("chatNav.header.autoRenameFailed"));
        return false;
      }
    },
    [apply, t],
  );

  const setLabels = useCallback(
    async (c: ConversationDTO, labels: string[]): Promise<boolean> => {
      try {
        apply(await updateLabels(c.publicID, labels));
        return true;
      } catch (error) {
        toast.error(t("chatNav.labels.failed"), { description: errorMessage(error) });
        return false;
      }
    },
    [apply, errorMessage, t],
  );

  const moveToProject = useCallback(
    async (c: ConversationDTO, projectId: string, projectName: string): Promise<boolean> => {
      try {
        apply(await setConversationProject(c.publicID, projectId));
        toast.success(t("chatNav.move.done", { project: projectName || t("chatNav.move.unassigned") }));
        return true;
      } catch (error) {
        toast.error(t("chatNav.move.failed"), { description: errorMessage(error) });
        return false;
      }
    },
    [apply, errorMessage, t],
  );

  const remove = useCallback(
    async (c: ConversationDTO, deleteFiles: boolean): Promise<void> => {
      await deleteInList(c.publicID, deleteFiles);
      chatEngine.forget(c.publicID);
      onDeleted?.(c.publicID);
    },
    [onDeleted],
  );

  const exportJson = useCallback(
    async (c: ConversationDTO): Promise<boolean> => {
      try {
        const doc = await exportConversation(c.publicID);
        downloadText(JSON.stringify(doc, null, 2), `conversation-${safeFileName(c.title, "conversation")}-${timestampForFile()}.json`, "application/json");
        toast.success(t("chatNav.header.exportDone"));
        return true;
      } catch (error) {
        toast.error(t("chatNav.header.exportFailed"), { description: errorMessage(error) });
        return false;
      }
    },
    [errorMessage, t],
  );

  const open = useCallback((id: string) => router.push(`/chat?conversation_id=${encodeURIComponent(id)}`), [router]);

  return { toggleStar, rename, autoRename, setLabels, moveToProject, remove, exportJson, open };
}
