"use client";

import { Sparkles, X } from "lucide-react";
import { Button, Spinner, Tooltip } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { isIndexOutdated } from "../shared/file-status";
import { formatBytes } from "../shared/format";
import { Checkbox } from "../shared/ui/checkbox";
import { FileIcon } from "../shared/ui/file-icon";
import { RetrievalBadge } from "../shared/ui/status-badge";
import type { KnowledgeBaseFile } from "../shared/types";

export interface KbFileRowProps {
  file: KnowledgeBaseFile;
  editable: boolean;
  checked: boolean;
  selectionActive: boolean;
  removing: boolean;
  removeLocked: boolean;
  submitting: boolean;
  vectorizeBusy: boolean;
  onOpen: () => void;
  onToggle: () => void;
  onRemove: () => void;
  onVectorize: () => void;
}

/** A file inside a knowledge base. The name opens the preview; the buttons act on the file. */
export function KbFileRow(props: KbFileRowProps) {
  const { file, editable, checked, selectionActive, removing, removeLocked, submitting, vectorizeBusy, onOpen, onToggle, onRemove, onVectorize } = props;
  const t = useT();
  const outdated = isIndexOutdated(file);
  const indexLabel = outdated ? t("kbWs.detail.refreshIndex") : t("kbWs.detail.buildIndex");
  const indexName = outdated
    ? t("kbWs.detail.refreshIndexNamed", { name: file.fileName })
    : t("kbWs.detail.buildIndexNamed", { name: file.fileName });
  return (
    <li className="group flex items-center gap-3 rounded-lg border bg-card px-3 py-2 transition-colors hover:bg-accent/40">
      {editable && file.canVectorize ? (
        <Checkbox
          checked={checked}
          onCheckedChange={onToggle}
          aria-label={t("kbWs.detail.rowCheckbox", { name: file.fileName })}
          className={cn(!checked && !selectionActive && "md:opacity-0 md:group-focus-within:opacity-100 md:group-hover:opacity-100")}
        />
      ) : editable ? (
        <span className="size-4 shrink-0" aria-hidden />
      ) : null}
      <button
        type="button"
        onClick={onOpen}
        title={file.fileName}
        className="flex min-w-0 flex-1 items-center gap-3 rounded-md text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <FileIcon file={file} className="size-5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1">
          <span className="block truncate text-sm font-medium">{file.fileName}</span>
          <span className="block text-xs text-muted-foreground">{formatBytes(file.sizeBytes)}</span>
        </span>
        <RetrievalBadge file={file} />
      </button>
      {editable ? (
        <div className="flex shrink-0 items-center gap-0.5">
          {file.canVectorize ? (
            <Tooltip label={indexLabel}>
              <Button variant="ghost" size="icon-xs" aria-label={indexName} disabled={vectorizeBusy} onClick={onVectorize}>
                {submitting ? <Spinner /> : <Sparkles />}
              </Button>
            </Tooltip>
          ) : null}
          <Tooltip label={t("kbWs.detail.remove")}>
            <Button variant="ghost" size="icon-xs" aria-label={t("kbWs.detail.removeNamed", { name: file.fileName })} disabled={removeLocked} onClick={onRemove} className="hover:text-destructive">
              {removing ? <Spinner /> : <X />}
            </Button>
          </Tooltip>
        </div>
      ) : null}
    </li>
  );
}
