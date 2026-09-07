"use client";

import { useState } from "react";
import { ArrowLeft, BookOpen, FilePlus2, RotateCw, Sparkles } from "lucide-react";
import { Badge, Button, EmptyState, Skeleton, Spinner, Switch } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import type { KnowledgeBase, KnowledgeBaseFile } from "../shared/types";
import { Checkbox } from "../shared/ui/checkbox";
import type { KbMode } from "./api";
import { KbFileRow } from "./kb-file-row";
import { KbPreviewDialog } from "./kb-preview-dialog";
import { headerCheckState, isEditable } from "./model";
import type { KbFiles } from "./use-kb-files";

export interface KbDetailProps {
  mode: KbMode;
  kb: KnowledgeBase | null;
  files: KbFiles;
  toggling: boolean;
  onBack?: () => void;
  onAddFiles: () => void;
  onToggleEnabled: (enabled: boolean) => void;
}

export function KbDetail({ mode, kb, files, toggling, onBack, onAddFiles, onToggleEnabled }: KbDetailProps) {
  const t = useT();
  const [previewing, setPreviewing] = useState<KnowledgeBaseFile | null>(null);

  if (!kb) {
    return (
      <div className="flex min-w-0 flex-1 flex-col">
        {onBack ? (
          <div className="border-b p-2">
            <Button variant="ghost" size="sm" onClick={onBack}>
              <ArrowLeft />
              {t("kbWs.detail.back")}
            </Button>
          </div>
        ) : null}
        <EmptyState icon={<BookOpen />} title={t("kbWs.detail.emptyTitle")} description={t("kbWs.detail.emptyBody")} className="flex-1" />
      </div>
    );
  }

  const editable = isEditable(kb, mode);
  const { state } = files;
  const picked = state.checked.size;
  const header = headerCheckState(state.checked, state.files);

  return (
    <div className="flex min-h-0 min-w-0 flex-1 flex-col">
      <header className="flex flex-wrap items-center gap-3 border-b px-4 py-3">
        {onBack ? (
          <Button variant="ghost" size="icon-sm" aria-label={t("kbWs.detail.back")} onClick={onBack}>
            <ArrowLeft />
          </Button>
        ) : null}
        <div className="min-w-0 flex-1">
          <div className="flex items-center gap-2">
            <h1 className="truncate text-base font-semibold" title={kb.name}>
              {kb.name}
            </h1>
            <Badge tone={kb.scope === "builtin" ? "primary" : "neutral"}>{kb.scope === "builtin" ? t("kbWs.badges.builtin") : t("kbWs.badges.personal")}</Badge>
          </div>
          <p className="truncate text-sm text-muted-foreground">{kb.description || t("kbWs.detail.fileCountFallback", { count: kb.fileCount })}</p>
        </div>
        {mode === "admin" ? (
          <label className="flex items-center gap-2 text-sm">
            <Switch
              checked={kb.enabled}
              disabled={toggling}
              aria-label={t("kbWs.detail.availability")}
              onCheckedChange={onToggleEnabled}
            />
            <span className="text-muted-foreground">{kb.enabled ? t("kbWs.detail.on") : t("kbWs.detail.off")}</span>
          </label>
        ) : null}
        {editable ? (
          <Button size="sm" onClick={onAddFiles}>
            <FilePlus2 />
            {t("kbWs.detail.addFiles")}
          </Button>
        ) : null}
      </header>

      {picked > 0 ? (
        <div className="flex items-center gap-3 border-b bg-accent/40 px-4 py-2">
          <Checkbox
            checked={header === "all"}
            indeterminate={header === "some"}
            onCheckedChange={(on) => files.setAllChecked(on)}
            aria-label={t("kbWs.detail.selectionAll")}
          />
          <span className="text-sm">{t("kbWs.detail.selectedCount", { count: picked })}</span>
          <div className="ml-auto flex items-center gap-2">
            <Button variant="ghost" size="sm" onClick={files.clearChecked}>
              {t("common.actions.cancel")}
            </Button>
            <Button size="sm" disabled={picked === 0 || files.vectorizeBusy} loading={files.vectorizeBusy} onClick={() => void files.vectorize([...state.checked])}>
              <Sparkles />
              {t("kbWs.detail.vectorizeSelected")}
            </Button>
          </div>
        </div>
      ) : null}

      <div className="min-h-0 flex-1 overflow-y-auto p-4">
        {state.loading ? (
          <div className="grid gap-2" aria-busy role="status" aria-label={t("common.states.loading")}>
            {Array.from({ length: 4 }, (_, i) => (
              <Skeleton key={i} className="h-14" />
            ))}
          </div>
        ) : state.failed ? (
          <EmptyState
            icon={<RotateCw />}
            title={t("kbWs.detail.filesFailedTitle")}
            description={t("kbWs.detail.filesFailedBody")}
            action={
              <Button variant="outline" size="sm" onClick={files.reload}>
                {t("common.actions.retry")}
              </Button>
            }
          />
        ) : state.files.length === 0 ? (
          <EmptyState
            icon={<BookOpen />}
            title={t("kbWs.detail.noFilesTitle")}
            description={editable ? t("kbWs.detail.noFilesHint") : undefined}
          />
        ) : (
          <ul className="grid gap-2">
            {state.files.map((file) => (
              <KbFileRow
                key={file.fileID}
                file={file}
                editable={editable}
                checked={state.checked.has(file.fileID)}
                selectionActive={picked > 0}
                removing={files.removingId === file.fileID}
                removeLocked={files.removingId !== null}
                submitting={files.submitting.has(file.fileID)}
                vectorizeBusy={files.vectorizeBusy}
                onOpen={() => setPreviewing(file)}
                onToggle={() => files.toggleChecked(file.fileID)}
                onRemove={() => void files.removeFile(file.fileID)}
                onVectorize={() => void files.vectorize([file.fileID])}
              />
            ))}
          </ul>
        )}
        {!state.loading && state.files.length < state.total ? (
          <div className="flex justify-center pt-3">
            <Button variant="outline" size="sm" disabled={state.loadingMore} onClick={files.loadMore}>
              {state.loadingMore ? <Spinner /> : null}
              {t("kbWs.detail.loadMore")}
            </Button>
          </div>
        ) : null}
      </div>

      <KbPreviewDialog mode={mode} kbId={kb.publicID} file={previewing} onClose={() => setPreviewing(null)} />
    </div>
  );
}
