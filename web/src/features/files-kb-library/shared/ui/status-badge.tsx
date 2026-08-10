"use client";

import { Badge } from "@/components/ui";
import { useT, type Translate } from "@/lib/i18n/provider";
import { processingBadge, retrievalLabel, type BadgeTone, type RetrievalKey, type StatusBadge } from "../file-status";
import type { StatusBearing } from "../types";

export const BADGE_TONE: Record<BadgeTone, "neutral" | "primary" | "success" | "warning" | "danger"> = {
  neutral: "neutral",
  info: "primary",
  success: "success",
  warning: "warning",
  danger: "danger",
};

export function statusText(t: Translate, key: StatusBadge["key"]): string {
  switch (key) {
    case "failed": return t("filesWs.status.failed");
    case "empty": return t("filesWs.status.empty");
    case "indexFailed": return t("filesWs.status.indexFailed");
    case "vectorizing": return t("filesWs.status.vectorizing");
    case "queued": return t("filesWs.status.queued");
    case "indexOutdated": return t("filesWs.status.indexOutdated");
    case "waiting": return t("filesWs.status.waiting");
    case "extracting": return t("filesWs.status.extracting");
    case "ocr": return t("filesWs.status.ocr");
    case "ready": return t("filesWs.status.ready");
    case "imageReady": return t("filesWs.status.imageReady");
    default: return t("filesWs.status.processing");
  }
}

export function retrievalText(t: Translate, key: RetrievalKey): string {
  switch (key) {
    case "disabled": return t("filesWs.retrieval.disabled");
    case "outdated": return t("filesWs.retrieval.outdated");
    case "empty": return t("filesWs.retrieval.empty");
    case "searchable": return t("filesWs.retrieval.searchable");
    case "vectorizing": return t("filesWs.retrieval.vectorizing");
    case "pending": return t("filesWs.retrieval.pending");
    default: return statusText(t, key);
  }
}

/** The file's overall processing state as a coloured pill. */
export function ProcessingBadge({ file, ocrUsed }: { file: StatusBearing; ocrUsed?: boolean }) {
  const t = useT();
  const badge = processingBadge({ ...file, ocrUsed });
  const detail =
    badge.key === "failed"
      ? badge.detail ?? (badge.errorCode ? t("filesWs.status.failedCode", { code: badge.errorCode }) : t("filesWs.status.failedGeneric"))
      : badge.key === "indexFailed"
        ? badge.detail
        : undefined;
  return (
    <Badge tone={BADGE_TONE[badge.tone]} title={detail}>
      {statusText(t, badge.key)}
      {/* A title alone is invisible to keyboards and touch screens. */}
      {detail ? <span className="sr-only">{`: ${detail}`}</span> : null}
    </Badge>
  );
}

/** How searchable a file inside a knowledge base is. */
export function RetrievalBadge({ file }: { file: StatusBearing }) {
  const t = useT();
  const label = retrievalLabel(file);
  return <Badge tone={BADGE_TONE[label.tone]}>{retrievalText(t, label.key)}</Badge>;
}
