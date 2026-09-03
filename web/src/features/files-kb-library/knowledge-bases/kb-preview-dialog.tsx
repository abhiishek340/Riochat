"use client";

import { Download, ExternalLink } from "lucide-react";
import { Button, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, Tooltip } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { downloadBlobUrl, openInNewTab } from "../shared/preview/actions";
import { PreviewPane } from "../shared/preview/preview-pane";
import { usePreview } from "../shared/preview/use-preview";
import type { KnowledgeBaseFile } from "../shared/types";
import { fetchKbContent, type KbMode } from "./api";

/** Preview of one file of a knowledge base, loaded through the base's own content endpoint. */
export function KbPreviewDialog({
  mode,
  kbId,
  file,
  onClose,
}: {
  mode: KbMode;
  kbId: string;
  file: KnowledgeBaseFile | null;
  onClose: () => void;
}) {
  const t = useT();
  const state = usePreview(
    file ? { id: file.fileID, fileName: file.fileName, mimeType: file.mimeType, updatedAt: file.updatedAt } : null,
    (signal) => fetchKbContent(mode, kbId, file?.fileID ?? "", signal),
  );
  const ready = state.phase === "ready";
  return (
    <Dialog open={file !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="flex h-[min(85dvh,52rem)] max-w-4xl flex-col gap-3">
        <DialogHeader>
          <DialogTitle className="truncate pr-6">{file?.fileName}</DialogTitle>
          <DialogDescription className="sr-only">{t("kbWs.detail.previewTitle")}</DialogDescription>
        </DialogHeader>
        <div className="flex justify-end gap-1">
          <Tooltip label={t("filesWs.header.open")}>
            <Button variant="outline" size="icon-sm" aria-label={t("filesWs.header.open")} disabled={!ready} onClick={() => ready && openInNewTab(state.openUrl)}>
              <ExternalLink />
            </Button>
          </Tooltip>
          <Tooltip label={t("filesWs.header.download")}>
            <Button
              variant="outline"
              size="icon-sm"
              aria-label={t("filesWs.header.download")}
              disabled={!ready}
              onClick={() => ready && file && downloadBlobUrl(state.openUrl, file.fileName)}
            >
              <Download />
            </Button>
          </Tooltip>
        </div>
        <div className="min-h-0 flex-1 overflow-auto rounded-xl border bg-card">
          {file ? <PreviewPane state={state} fileName={file.fileName} /> : null}
        </div>
      </DialogContent>
    </Dialog>
  );
}
