"use client";

import { AlertTriangle, FileText, Loader2, TextCursorInput, X } from "lucide-react";
import { Badge, Tooltip } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import type { UseDraft } from "../../hooks/use-draft";
import { attachmentFailed } from "../../lib/attachments";
import { isProcessing } from "../../lib/file-policy";
import { formatBytes } from "../../lib/format";
import type { ChatAttachment } from "../../types";

export function AttachmentChips({
  draft,
  fileMode,
  onInsertBack,
  pastedTexts,
}: {
  draft: UseDraft;
  fileMode: string;
  onInsertBack: (a: ChatAttachment) => void;
  pastedTexts: ReadonlySet<string>;
}) {
  const t = useT();
  if (draft.attachments.length === 0 && draft.uploading.length === 0) return null;
  return (
    <ul className="flex flex-wrap gap-2 px-3 pt-3" aria-label={t("chatComposer.attach.list")}>
      {draft.attachments.map((a) => {
        const failed = attachmentFailed(a);
        const processing = !failed && (isProcessing(a.processingStatus, a.embedStatus) || (!a.processingReady && a.processingStatus !== "" && a.processingStatus !== "ready"));
        const ragOff = fileMode === "rag" && a.kind !== "image" && a.ragOptOut;
        return (
          <li key={a.fileId} className={cn("group/chip relative flex max-w-56 items-center gap-2 rounded-xl border bg-muted/40 py-1.5 pl-1.5 pr-7 text-xs", failed && "border-destructive/50")}>
            {a.kind === "image" && a.previewUrl ? (
              // eslint-disable-next-line @next/next/no-img-element -- local preview of the chosen file
              <img src={a.previewUrl} alt={t("chatComposer.attach.preview", { name: a.fileName })} className="size-9 rounded-lg object-cover" />
            ) : (
              <span className="flex size-9 items-center justify-center rounded-lg bg-background">
                <FileText className="size-4 text-muted-foreground" />
              </span>
            )}
            <span className="min-w-0">
              <span className="block truncate font-medium" title={a.fileName}>
                {a.fileName}
              </span>
              <span className="flex flex-wrap items-center gap-1 text-muted-foreground">
                {a.fileSize > 0 ? formatBytes(a.fileSize) : null}
                {processing ? (
                  <Badge tone="primary" className="py-0">
                    <Loader2 className="size-3 animate-spin" />
                    {t("chatComposer.attach.processing")}
                  </Badge>
                ) : null}
                {failed ? (
                  <Tooltip label={a.processingErrorMessage || a.processingErrorCode}>
                    <Badge tone="danger" className="py-0">
                      <AlertTriangle className="size-3" />
                      {t("chatComposer.attach.failed")}
                    </Badge>
                  </Tooltip>
                ) : null}
                {ragOff ? (
                  <Tooltip label={t("chatComposer.attach.ragOffTip")}>
                    <Badge tone="warning" className="py-0">
                      {t("chatComposer.attach.ragOff")}
                    </Badge>
                  </Tooltip>
                ) : null}
              </span>
            </span>
            {pastedTexts.has(a.fileId) ? (
              <Tooltip label={t("chatComposer.attach.insertTip")}>
                <button type="button" onClick={() => onInsertBack(a)} aria-label={t("chatComposer.attach.insertText", { name: a.fileName })} className="absolute bottom-1 right-1 rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground">
                  <TextCursorInput className="size-3.5" />
                </button>
              </Tooltip>
            ) : null}
            <Tooltip label={t("chatComposer.attach.removeTip")}>
              <button type="button" onClick={() => draft.removeAttachment(a.fileId)} aria-label={t("chatComposer.attach.remove", { name: a.fileName })} className="absolute right-1 top-1 rounded-full bg-background p-0.5 text-muted-foreground shadow-sm hover:text-foreground">
                <X className="size-3" />
              </button>
            </Tooltip>
          </li>
        );
      })}
      {draft.uploading.map((u) => (
        <li key={u.id} className="flex max-w-56 items-center gap-2 rounded-xl border border-dashed bg-muted/30 px-2.5 py-2 text-xs text-muted-foreground" aria-busy>
          <Loader2 className="size-4 animate-spin" />
          <span className="truncate">{t("chatComposer.attach.uploadingName", { name: u.name })}</span>
        </li>
      ))}
    </ul>
  );
}
