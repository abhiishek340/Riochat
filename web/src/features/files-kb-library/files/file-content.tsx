"use client";

import { useState } from "react";
import { ArrowLeft, Download, ExternalLink, Info, Sparkles, Trash2, FileText, ScanText } from "lucide-react";
import { Button, EmptyState, Spinner, Tabs, TabsContent, TabsList, TabsTrigger, Tooltip } from "@/components/ui";
import { useI18n } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { isIndexOutdated } from "../shared/file-status";
import { formatBytes, formatDateTime, nameInitials } from "../shared/format";
import { rawTypeOf } from "../shared/file-kind";
import { downloadBlobUrl, openInNewTab } from "../shared/preview/actions";
import { PlainText, PreviewPane } from "../shared/preview/preview-pane";
import { usePreview } from "../shared/preview/use-preview";
import { FileIcon } from "../shared/ui/file-icon";
import { ProcessingBadge } from "../shared/ui/status-badge";
import type { FileObject } from "../shared/types";
import { fetchContent } from "./api";
import { FileInfoDialog } from "./file-info-dialog";
import { useExtract } from "./use-extract";

export type ContentTab = "preview" | "extract";

export interface FileContentProps {
  file: FileObject | null;
  /** The list is still loading or syncing: show a neutral placeholder instead of the empty state. */
  waiting: boolean;
  tab: ContentTab;
  onTabChange: (tab: ContentTab) => void;
  onBack?: () => void;
  deleting: boolean;
  submitting: boolean;
  vectorizeBusy: boolean;
  onDelete: () => void;
  onVectorize: () => void;
  onToggleRag: () => void;
}

function Thumb({ file, imageUrl }: { file: FileObject; imageUrl: string | null }) {
  return (
    <div className="relative flex size-11 shrink-0 items-center justify-center overflow-hidden rounded-lg border bg-muted text-muted-foreground">
      {imageUrl ? (
        // eslint-disable-next-line @next/next/no-img-element -- local blob URL
        <img src={imageUrl} alt="" className="size-full object-cover" />
      ) : (
        <>
          <FileIcon file={file} className="size-5" />
          <span className="absolute bottom-0 right-0 rounded-tl bg-background/80 px-0.5 text-[9px] font-semibold leading-3">{nameInitials(file.fileName)}</span>
        </>
      )}
    </div>
  );
}

export function FileContent(props: FileContentProps) {
  const { file, waiting, tab, onTabChange, onBack, deleting, submitting, vectorizeBusy, onDelete, onVectorize, onToggleRag } = props;
  const { t, locale } = useI18n();
  const [infoOpen, setInfoOpen] = useState(false);

  const preview = usePreview(
    file ? { id: file.fileID, fileName: file.fileName, mimeType: file.mimeType, status: file.status, updatedAt: file.updatedAt } : null,
    (signal) => fetchContent(file?.fileID ?? "", signal),
  );
  const extract = useExtract(file, tab === "extract");

  if (!file) {
    return (
      <div className="flex min-w-0 flex-1 flex-col">
        {onBack ? (
          <div className="border-b p-2">
            <Button variant="ghost" size="sm" onClick={onBack}>
              <ArrowLeft />
              {t("filesWs.header.back")}
            </Button>
          </div>
        ) : null}
        {waiting ? (
          <div className="flex flex-1 items-center justify-center text-muted-foreground" role="status">
            <Spinner label={t("common.states.loading")} className="size-5" />
          </div>
        ) : (
          <EmptyState
            icon={<FileText />}
            title={t("filesWs.empty.noSelectionTitle")}
            description={t("filesWs.empty.noSelectionBody")}
            className="flex-1"
          />
        )}
      </div>
    );
  }

  const ready = preview.phase === "ready";
  const rawType = rawTypeOf(file) || t("filesWs.header.unknownType");
  const meta = [formatDateTime(file.createdAt, locale), rawType, formatBytes(file.sizeBytes)].join(t("filesWs.header.metaSeparator"));
  const outdated = isIndexOutdated(file);
  const indexLabel = outdated ? t("filesWs.actions.refreshIndex") : t("filesWs.actions.buildIndex");

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <header className="flex flex-wrap items-center gap-3 border-b px-4 py-3">
        {onBack ? (
          <Button variant="ghost" size="icon-sm" aria-label={t("filesWs.header.back")} onClick={onBack}>
            <ArrowLeft />
          </Button>
        ) : null}
        <Thumb file={file} imageUrl={ready && preview.kind === "image" ? preview.url : null} />
        <div className="min-w-0 flex-1">
          <h1 className="truncate text-base font-semibold" title={file.fileName}>
            {file.fileName}
          </h1>
          <p className="truncate text-xs text-muted-foreground">{meta}</p>
        </div>
        <div className="flex flex-wrap items-center gap-2">
          <ProcessingBadge file={file} ocrUsed={extract.phase === "ready" ? extract.data.ocrUsed : undefined} />
          {file.embedStatus === "ready" ? (
            <Tooltip label={file.ragOptOut ? t("filesWs.header.ragOffTip") : t("filesWs.header.ragOnTip")}>
              <button
                type="button"
                onClick={onToggleRag}
                aria-pressed={!file.ragOptOut}
                className={cn(
                  "rounded-full border px-2 py-0.5 text-xs font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring",
                  file.ragOptOut ? "text-muted-foreground hover:bg-accent" : "border-primary/40 bg-primary/10 text-primary hover:bg-primary/15",
                )}
              >
                {file.ragOptOut ? t("filesWs.header.ragOff") : t("filesWs.header.ragOn")}
              </button>
            </Tooltip>
          ) : null}
        </div>
        <div className="flex items-center gap-1">
          {file.canVectorize ? (
            <Tooltip label={indexLabel}>
              <Button variant="outline" size="icon-sm" aria-label={indexLabel} disabled={vectorizeBusy} loading={submitting} onClick={onVectorize}>
                <Sparkles />
              </Button>
            </Tooltip>
          ) : null}
          <Tooltip label={t("filesWs.header.open")}>
            <Button variant="outline" size="icon-sm" aria-label={t("filesWs.header.open")} disabled={!ready} onClick={() => ready && openInNewTab(preview.openUrl)}>
              <ExternalLink />
            </Button>
          </Tooltip>
          <Tooltip label={t("filesWs.header.download")}>
            <Button variant="outline" size="icon-sm" aria-label={t("filesWs.header.download")} disabled={!ready} onClick={() => ready && downloadBlobUrl(preview.openUrl, file.fileName)}>
              <Download />
            </Button>
          </Tooltip>
          <Tooltip label={t("filesWs.row.delete")}>
            <Button variant="outline" size="icon-sm" aria-label={t("filesWs.row.delete")} loading={deleting} onClick={onDelete} className="hover:text-destructive">
              <Trash2 />
            </Button>
          </Tooltip>
        </div>
      </header>

      <Tabs value={tab} onValueChange={(v) => onTabChange(v === "extract" ? "extract" : "preview")} className="flex min-h-0 flex-1 flex-col">
        <div className="px-4 pt-3">
          <TabsList>
            <TabsTrigger value="preview">{t("filesWs.tabs.preview")}</TabsTrigger>
            <TabsTrigger value="extract">
              <ScanText className="size-3.5" />
              {t("filesWs.tabs.extract")}
            </TabsTrigger>
          </TabsList>
        </div>
        {/* Real tab panels, so each tab names the region it controls. */}
        <TabsContent value="preview" className="relative m-4 min-h-0 flex-1 overflow-auto rounded-xl border bg-card" data-testid="content-area">
          <PreviewPane state={preview} fileName={file.fileName} />
        </TabsContent>
        <TabsContent value="extract" className="relative m-4 min-h-0 flex-1 overflow-auto rounded-xl border bg-card" data-testid="content-area">
          <ExtractView state={extract} />
        </TabsContent>
      </Tabs>

      <div className="flex justify-end border-t px-4 py-2">
        <Button variant="ghost" size="sm" onClick={() => setInfoOpen(true)}>
          <Info />
          {t("filesWs.header.details")}
        </Button>
      </div>
      <FileInfoDialog file={file} open={infoOpen} onOpenChange={setInfoOpen} />
    </div>
  );
}

function ExtractView({ state }: { state: ReturnType<typeof useExtract> }) {
  const { t } = useI18n();
  if (state.phase === "idle") return null;
  if (state.phase === "loading") {
    return (
      <div className="flex h-full min-h-48 items-center justify-center gap-2 text-sm text-muted-foreground" role="status">
        <Spinner />
        {t("filesWs.preview.loading")}
      </div>
    );
  }
  if (state.phase === "blocked") {
    return (
      <EmptyState
        icon={<ScanText />}
        title={t("filesWs.extract.emptyTitle")}
        description={state.reason === "failed" ? t("filesWs.extract.failedProcessing") : t("filesWs.extract.notReady")}
        className="h-full"
      />
    );
  }
  if (state.phase === "error") {
    return <EmptyState icon={<ScanText />} title={t("filesWs.extract.emptyTitle")} description={state.message || t("filesWs.extract.loadFailed")} className="h-full" />;
  }
  const { data } = state;
  if (data.extractText.trim() === "") {
    return <EmptyState icon={<ScanText />} title={t("filesWs.extract.emptyTitle")} description={t("filesWs.extract.emptyBody")} className="h-full" />;
  }
  return (
    <div className="flex min-h-full flex-col">
      <p className="sticky top-0 border-b bg-card/95 px-4 py-2 text-xs text-muted-foreground backdrop-blur">
        {[
          t("filesWs.extract.stats", { chars: data.extractChars || data.extractText.length }),
          data.extractPages > 0 ? t("filesWs.extract.pages", { pages: data.extractPages }) : "",
          data.ocrUsed ? t("filesWs.extract.ocr") : "",
        ]
          .filter(Boolean)
          .join(" · ")}
      </p>
      <PlainText text={data.extractText} />
    </div>
  );
}
