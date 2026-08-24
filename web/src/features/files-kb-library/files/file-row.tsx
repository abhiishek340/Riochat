"use client";

import { useEffect, useRef, useState } from "react";
import { MoreHorizontal, SearchCheck, SearchX, Trash2 } from "lucide-react";
import { Button, DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, Input, Spinner, Tooltip } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { isIndexOutdated } from "../shared/file-status";
import { Checkbox } from "../shared/ui/checkbox";
import { FileIcon } from "../shared/ui/file-icon";
import type { FileObject } from "../shared/types";

export interface FileRowProps {
  file: FileObject;
  active: boolean;
  checked: boolean;
  submitting: boolean;
  vectorizeBusy: boolean;
  onOpen: () => void;
  onToggleChecked: () => void;
  onDelete: () => void;
  onVectorize: () => void;
  /** Resolves true when the editor must stay open. */
  onRename: (name: string) => Promise<boolean>;
}

/** One entry of the file list: select box, name, retrieval hint and hover actions. */
export function FileRow(props: FileRowProps) {
  const { file, active, checked, submitting, vectorizeBusy, onOpen, onToggleChecked, onDelete, onVectorize, onRename } = props;
  const t = useT();
  const [editing, setEditing] = useState(false);
  const [draft, setDraft] = useState(file.fileName);
  const [menuOpen, setMenuOpen] = useState(false);
  const committing = useRef(false);
  const cancelled = useRef(false);
  const startEditing = useRef(false);
  const input = useRef<HTMLInputElement>(null);

  useEffect(() => {
    if (editing) input.current?.select();
  }, [editing]);

  const commit = async () => {
    // Escape unmounts the field, which can still deliver a blur: that must not save.
    if (committing.current || cancelled.current) return;
    committing.current = true;
    try {
      const keepOpen = await onRename(draft);
      if (!keepOpen) setEditing(false);
    } finally {
      committing.current = false;
    }
  };

  const showRag = file.fileCategory !== "image" && file.embedStatus === "ready";
  const outdated = isIndexOutdated(file);

  return (
    <li
      data-active={active || undefined}
      className={cn(
        "group relative flex items-center gap-2 rounded-lg px-2 py-1.5 transition-colors hover:bg-accent/60",
        active && "bg-accent",
      )}
    >
      <Checkbox
        checked={checked}
        onCheckedChange={onToggleChecked}
        aria-label={t("filesWs.row.checkbox", { name: file.fileName })}
      />
      {editing ? (
        <Input
          ref={input}
          autoFocus
          value={draft}
          aria-label={t("filesWs.row.renameInput")}
          className="h-7 flex-1 px-2"
          onChange={(e) => setDraft(e.target.value)}
          onKeyDown={(e) => {
            if (e.key === "Enter") {
              e.preventDefault();
              void commit();
            } else if (e.key === "Escape") {
              e.preventDefault();
              cancelled.current = true;
              setEditing(false);
            }
          }}
          onBlur={() => void commit()}
        />
      ) : (
        <button
          type="button"
          onClick={onOpen}
          aria-current={active || undefined}
          className="flex min-w-0 flex-1 items-center gap-2 rounded-md py-1 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <FileIcon file={file} className="size-4 shrink-0 text-muted-foreground" />
          <span className="min-w-0 flex-1 truncate text-sm" title={file.fileName}>
            {file.fileName}
          </span>
          {showRag ? (
            <Tooltip label={file.ragOptOut ? t("filesWs.row.ragOff") : t("filesWs.row.ragOn")}>
              <span className="shrink-0 text-muted-foreground">
                {file.ragOptOut ? <SearchX aria-hidden className="size-3.5" /> : <SearchCheck aria-hidden className="size-3.5 text-primary" />}
                <span className="sr-only">{file.ragOptOut ? t("filesWs.row.ragOff") : t("filesWs.row.ragOn")}</span>
              </span>
            </Tooltip>
          ) : null}
        </button>
      )}
      {!editing ? (
        <div
          className={cn(
            "flex shrink-0 items-center gap-0.5 transition-opacity md:opacity-0 md:group-focus-within:opacity-100 md:group-hover:opacity-100",
            (menuOpen || submitting) && "md:opacity-100",
          )}
        >
          {submitting ? <Spinner className="mx-1.5" /> : null}
          <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-xs" aria-label={t("filesWs.row.moreNamed", { name: file.fileName })}>
                <MoreHorizontal />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent
              onCloseAutoFocus={(e) => {
                // Hand focus to the name editor instead of the menu button.
                if (startEditing.current) {
                  e.preventDefault();
                  startEditing.current = false;
                  setEditing(true);
                }
              }}
            >
              <DropdownMenuItem onSelect={onToggleChecked}>{checked ? t("filesWs.row.deselect") : t("filesWs.row.select")}</DropdownMenuItem>
              <DropdownMenuItem
                onSelect={() => {
                  setDraft(file.fileName);
                  cancelled.current = false;
                  startEditing.current = true;
                }}
              >
                {t("filesWs.row.rename")}
              </DropdownMenuItem>
              {file.canVectorize ? (
                <DropdownMenuItem disabled={vectorizeBusy} onSelect={onVectorize}>
                  {outdated ? t("filesWs.row.refreshIndex") : t("filesWs.row.buildIndex")}
                </DropdownMenuItem>
              ) : null}
            </DropdownMenuContent>
          </DropdownMenu>
          <Tooltip label={t("filesWs.row.delete")}>
            <Button variant="ghost" size="icon-xs" aria-label={t("filesWs.row.deleteNamed", { name: file.fileName })} onClick={onDelete} className="hover:text-destructive">
              <Trash2 />
            </Button>
          </Tooltip>
        </div>
      ) : null}
    </li>
  );
}
