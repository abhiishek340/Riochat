"use client";

import type { ReactNode } from "react";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui";
import { useI18n } from "@/lib/i18n/provider";
import { embedLabelKey, extractLabelKey } from "../shared/file-status";
import { formatBytes, formatDateTime } from "../shared/format";
import { ProcessingBadge } from "../shared/ui/status-badge";
import type { FileObject } from "../shared/types";

function Row({ label, children, mono = false }: { label: string; children: ReactNode; mono?: boolean }) {
  return (
    <div className="grid gap-0.5 border-b py-2 last:border-b-0 sm:grid-cols-[9rem_1fr] sm:gap-3">
      <dt className="text-xs font-medium text-muted-foreground">{label}</dt>
      <dd className={mono ? "break-all font-mono text-xs" : "break-words text-sm"}>{children}</dd>
    </div>
  );
}

/** The "more info" sheet: identifiers, statuses, sizes and dates for one file. */
export function FileInfoDialog({ file, open, onOpenChange }: { file: FileObject; open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t, locale } = useI18n();
  const embedKey = embedLabelKey(file.embedStatus);
  const extractKey = extractLabelKey(file.extractStatus);
  const embedText = {
    ready: t("filesWs.embed.ready"),
    processing: t("filesWs.embed.processing"),
    queued: t("filesWs.embed.queued"),
    failed: t("filesWs.embed.failed"),
    empty: t("filesWs.embed.empty"),
    pending: t("filesWs.embed.pending"),
  }[embedKey];
  const extractText = {
    ready: t("filesWs.extractStatus.ready"),
    processing: t("filesWs.extractStatus.processing"),
    failed: t("filesWs.extractStatus.failed"),
    pending: t("filesWs.extractStatus.pending"),
  }[extractKey];

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("filesWs.info.title")}</DialogTitle>
          <DialogDescription className="truncate">{file.fileName}</DialogDescription>
        </DialogHeader>
        <dl>
          <Row label={t("filesWs.info.id")} mono>{file.fileID}</Row>
          <Row label={t("filesWs.info.category")}>{file.fileCategory || "-"}</Row>
          <Row label={t("filesWs.info.mime")}>{file.detectedMIME || file.mimeType || "-"}</Row>
          <Row label={t("filesWs.info.processing")}><ProcessingBadge file={file} /></Row>
          <Row label={t("filesWs.info.extract")}>{extractText}</Row>
          <Row label={t("filesWs.info.vector")}>{embedText}</Row>
          {file.chunkCount > 0 ? <Row label={t("filesWs.info.chunks")}>{file.chunkCount}</Row> : null}
          <Row label={t("filesWs.info.size")}>{formatBytes(file.sizeBytes)}</Row>
          <Row label={t("filesWs.info.purpose")}>{file.purpose || t("filesWs.info.purposeNone")}</Row>
          <Row label={t("filesWs.info.uploaded")}>{formatDateTime(file.createdAt, locale)}</Row>
          <Row label={t("filesWs.info.updated")}>{formatDateTime(file.updatedAt, locale)}</Row>
          <Row label={t("filesWs.info.expires")}>{formatDateTime(file.expiresAt, locale)}</Row>
          <Row label={t("filesWs.info.sha")} mono>{file.sha256 || "-"}</Row>
          {file.processingErrorMessage ? <Row label={t("filesWs.info.failure")}>{file.processingErrorMessage}</Row> : null}
          {file.embedError ? <Row label={t("filesWs.info.indexError")}>{file.embedError}</Row> : null}
        </dl>
      </DialogContent>
    </Dialog>
  );
}
