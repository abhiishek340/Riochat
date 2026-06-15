"use client";

import { useMemo, useRef } from "react";
import { useRouter } from "next/navigation";
import { toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { deleteMessage, editMessage, forkConversation, renameConversation, savePreferenceMemory, setFeedback } from "../api";
import { forkTitle } from "../lib/text";
import { parseKinds } from "../lib/task-decision";
import { modelFacts, parseModelCaps } from "../lib/model-caps";
import { chatEngine } from "../state/engine-instance";
import { tempEngine } from "../state/temp-engine";
import { findConversation, upsertConversation } from "../state/conversation-store";
import { protectedFileId } from "./use-image-source";
import type { MessageActions } from "../components/message/env";
import type { ChatAttachment, ChatMessage, ModelDTO, ModelOptionPolicyDTO } from "../types";
import type { SendParams } from "../state/engine-types";
import type { ChatView } from "../state/engine-types";
import type { UseDraft } from "./use-draft";
import type { ModelState } from "./use-model-state";
import type { ArtifactKind } from "../lib/artifacts";

type SendBase = Omit<SendParams, "text" | "attachments" | "parentId" | "sourceId" | "branchReason" | "assistantOnly" | "answeringUserId" | "fromComposer" | "restore" | "runId">;

export interface ChatActionsInput {
  chatKey: string;
  temporary: boolean;
  view: ChatView;
  models: ModelDTO[];
  policy: ModelOptionPolicyDTO | null;
  draft: UseDraft;
  model: ModelState;
  base: () => SendBase;
  tempSend: (text: string, attachments: ChatAttachment[]) => void;
  continueText: string;
  openCode: (m: ChatMessage, args: { kind: ArtifactKind; code: string; lang: string }) => void;
  openBlock: (m: ChatMessage, key: string) => void;
  requestDelete: (m: ChatMessage) => void;
  composerInsert: (text: string) => void;
  lastPrefill: React.MutableRefObject<string>;
  forkSuffix: string;
}

export function useChatActions(input: ChatActionsInput): MessageActions {
  const t = useT();
  const router = useRouter();
  const errorMessage = useErrorMessage();
  const ref = useRef(input);
  ref.current = input;
  // A fork already on its way: a second click does nothing.
  const forking = useRef(false);

  return useMemo<MessageActions>(() => {
    const cur = () => ref.current;
    const persisted = (m: ChatMessage) => m.persisted && !!m.id && !m.id.startsWith("local-");

    const send = (extra: Pick<SendParams, "text" | "attachments" | "parentId" | "sourceId" | "branchReason"> & Partial<SendParams>) => {
      void chatEngine.send({ ...cur().base(), ...extra });
    };

    const modelSupports = (m: ModelDTO | null | undefined, kind: "image_edit" | "extension"): boolean => {
      if (!m) return false;
      if (kind === "image_edit") return parseKinds(m.kindsJSON).has("image_edit");
      return modelFacts(m, parseModelCaps(m, cur().policy)).supportsVideoExtension;
    };

    const actions: MessageActions = {
      retry: (m) => {
        const c = cur();
        if (c.temporary) {
          const list = c.view.path;
          if (m.role === "assistant") {
            const at = list.findIndex((x) => x.id === m.id);
            const user = [...list.slice(0, at)].reverse().find((x) => x.role === "user");
            if (!user) return;
            tempEngine.truncateBefore(user.id);
            c.tempSend(user.content, user.attachments);
          } else {
            tempEngine.truncateBefore(m.id);
            c.tempSend(m.content, m.attachments);
          }
          return;
        }
        if (!persisted(m)) return void toast.error(t("chatMsg.toast.cannotRetry"));
        if (m.role === "assistant") {
          const user = c.view.messages.find((x) => x.id === m.parentId && x.role === "user");
          if (!user) return void toast.error(t("chatMsg.toast.retryNoUser"));
          send({ text: user.content, attachments: user.attachments, parentId: user.id, sourceId: m.id, branchReason: "retry", assistantOnly: true, answeringUserId: user.id });
        } else {
          send({ text: m.content, attachments: m.attachments, parentId: m.parentId, sourceId: m.id, branchReason: "retry" });
        }
      },
      continueReply: (m) => {
        const c = cur();
        if (m.status !== "interrupted" || !persisted(m)) return void toast.error(t("chatMsg.toast.cannotContinue"));
        send({ text: c.continueText, attachments: [], parentId: m.id, sourceId: null, branchReason: "default" });
      },
      saveEdit: async (m, text) => {
        const c = cur();
        if (!text.trim()) {
          toast.error(t("chatMsg.toast.editFailed"));
          return false;
        }
        if (c.temporary) {
          if (m.role === "assistant") tempEngine.truncateAfter(m.id);
          tempEngine.editLocal(m.id, text, m.role === "assistant");
          return true;
        }
        try {
          const dto = await editMessage(m.id, text);
          chatEngine.replaceServerMessage(c.chatKey, dto);
          return true;
        } catch (error) {
          toast.error(t("chatMsg.toast.editFailed"), { description: errorMessage(error) });
          return false;
        }
      },
      regenerate: (m, text) => {
        const c = cur();
        if (c.temporary) {
          tempEngine.truncateBefore(m.id);
          c.tempSend(text, m.attachments);
          return;
        }
        if (!persisted(m)) return void toast.error(t("chatMsg.toast.cannotRetry"));
        send({ text, attachments: m.attachments, parentId: m.parentId, sourceId: m.id, branchReason: "edit" });
      },
      remove: (m) => cur().requestDelete(m),
      fork: async (m) => {
        const c = cur();
        if (c.temporary || !persisted(m) || forking.current) return;
        forking.current = true;
        try {
          const created = await forkConversation(c.chatKey, m.id);
          const source = findConversation(c.chatKey);
          const title = forkTitle(source?.title || created.title || "", c.forkSuffix);
          let result = created;
          try {
            result = await renameConversation(created.publicID, title);
          } catch {
            /* the forked conversation is used as returned */
          }
          upsertConversation(result);
          router.push(`/chat?conversation_id=${encodeURIComponent(result.publicID)}`);
        } catch (error) {
          toast.error(t("chatMsg.toast.forkFailed"), { description: errorMessage(error) });
        } finally {
          forking.current = false;
        }
      },
      feedback: async (m, value) => {
        const c = cur();
        const next = m.feedback === value ? "" : value;
        if (c.temporary) {
          tempEngine.patch(m.id, { feedback: next });
          toast.success(next === "up" ? t("chatMsg.toast.liked") : next === "down" ? t("chatMsg.toast.disliked") : t("chatMsg.toast.feedbackCleared"));
          return;
        }
        if (!persisted(m)) return;
        chatEngine.patchMessage(c.chatKey, m.id, { feedback: next });
        try {
          const res = await setFeedback(m.id, next);
          chatEngine.patchMessage(c.chatKey, m.id, { feedback: res.myFeedback === "up" || res.myFeedback === "down" ? res.myFeedback : "" });
        } catch (error) {
          chatEngine.patchMessage(c.chatKey, m.id, { feedback: m.feedback });
          toast.error(t("chatMsg.toast.feedbackFailed"), { description: errorMessage(error) });
        }
      },
      cycle: (m, delta) => {
        const c = cur();
        if (!c.temporary) chatEngine.cycle(c.chatKey, m.id, delta);
      },
      openCode: (m, args) => cur().openCode(m, args),
      openBlock: (m, key) => cur().openBlock(m, key),
      imageEditable: (src) => {
        const id = protectedFileId(src);
        return !!id && cur().view.path.some((m) => m.attachments.some((a) => a.fileId === id));
      },
      editImage: (src, alt) => {
        const c = cur();
        const id = protectedFileId(src);
        const attachment = id ? c.view.path.flatMap((m) => m.attachments).find((a) => a.fileId === id) : undefined;
        if (!attachment) return void toast.error(t("chatRich.md.imageUnavailable"), { description: alt });
        const have = c.draft.attachments.some((a) => a.fileId === attachment.fileId);
        if (!have) c.draft.addExisting([{ ...attachment, previewUrl: undefined }]);
        if (!modelSupports(c.model.model, "image_edit")) {
          const source = c.view.path.find((m) => m.attachments.some((a) => a.fileId === attachment.fileId))?.model;
          const sourceModel = c.models.find((x) => x.platformModelName === source);
          const target = modelSupports(sourceModel, "image_edit") ? sourceModel : c.models.find((x) => modelSupports(x, "image_edit"));
          if (target) c.model.select(target.platformModelName);
        }
      },
      canExtendVideo: (m) => {
        const c = cur();
        return m.attachments.some((a) => (a.mimeType === "video/mp4" || a.detectedMime === "video/mp4")) && c.models.some((x) => modelSupports(x, "extension"));
      },
      extendVideo: (a, m) => {
        const c = cur();
        const source = c.models.find((x) => x.platformModelName === m.model);
        const target = modelSupports(source, "extension") ? source : modelSupports(c.model.model, "extension") ? c.model.model : c.models.find((x) => modelSupports(x, "extension"));
        if (!target) return void toast.error(t("chatMsg.toast.extendUnsupported"));
        c.draft.clear();
        c.draft.addExisting([{ ...a, previewUrl: undefined }]);
        c.model.select(target.platformModelName);
      },
      rememberPreference: async (name, value) => {
        try {
          await savePreferenceMemory(name, value);
          toast.success(t("chatMsg.remember.success"), { description: t("chatMsg.remember.successDescription") });
          return true;
        } catch (error) {
          toast.error(t("chatMsg.remember.failed"), { description: errorMessage(error) });
          return false;
        }
      },
      loadFullTrace: () => undefined,
      ask: (text) => {
        const c = cur();
        const trimmed = text.trim();
        if (!trimmed || c.view.busy) return;
        if (c.temporary) return c.tempSend(trimmed, []);
        const leaf = [...c.view.path].reverse().find((m) => m.persisted && (m.status === "success" || (m.role === "assistant" && m.status === "interrupted")));
        send({ text: trimmed, attachments: [], parentId: leaf?.id ?? null, sourceId: null, branchReason: "default" });
      },
      prefill: (text) => {
        const c = cur();
        const trimmed = text.trim();
        if (!trimmed) return;
        const current = c.draft.text;
        if (current.trim() === "" || current === c.lastPrefill.current) {
          c.draft.setText(trimmed);
          c.lastPrefill.current = trimmed;
          toast.success(t("chatRich.ui.prefilled"));
        } else toast.info(t("chatRich.ui.prefillSkipped"));
      },
    };
    return actions;
  }, [t, router, errorMessage]);
}

/** Confirmed deletion of a message; the list reloads afterwards. */
export async function removeMessage(chatKey: string, m: ChatMessage): Promise<void> {
  await deleteMessage(chatKey, m.id);
  await chatEngine.load(chatKey, { reload: true });
}
