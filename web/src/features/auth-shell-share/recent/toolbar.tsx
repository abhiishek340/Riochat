"use client";

import { Archive, ArchiveRestore, CheckSquare, Download, FolderInput, Link2Off, Trash2, X } from "lucide-react";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Tooltip } from "@/components/ui/tooltip";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import type { ProjectRef } from "./api";
import { Checkbox } from "./checkbox";
import type { ArchiveAction } from "./model";

export function Segmented<T extends string>({
  label,
  value,
  options,
  onChange,
}: {
  label: string;
  value: T;
  options: { value: T; label: string }[];
  onChange: (value: T) => void;
}) {
  return (
    <div role="radiogroup" aria-label={label} className="inline-flex h-8 items-center rounded-lg bg-muted p-0.5">
      {options.map((o) => (
        <button
          key={o.value}
          type="button"
          role="radio"
          aria-checked={value === o.value}
          onClick={() => onChange(o.value)}
          className={cn(
            "h-7 rounded-md px-2.5 text-xs font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring",
            value === o.value ? "bg-background text-foreground shadow-sm" : "text-muted-foreground hover:text-foreground",
          )}
        >
          {o.label}
        </button>
      ))}
    </div>
  );
}

export function SelectionBar({
  count,
  sharedCount,
  sharingEnabled,
  archiveAction,
  projects,
  onMove,
  onStopSharing,
  onArchive,
  onDelete,
  onCancel,
}: {
  count: number;
  sharedCount: number;
  sharingEnabled: boolean;
  archiveAction: ArchiveAction;
  projects: ProjectRef[];
  onMove: (projectId: string) => void;
  onStopSharing: () => void;
  onArchive: () => void;
  onDelete: () => void;
  onCancel: () => void;
}) {
  const t = useT();
  const none = count === 0;
  return (
    <div className="flex items-center gap-1">
      <span className="mr-1 text-sm font-medium" aria-live="polite">
        {t("recentAll.bulk.selected", { count })}
      </span>
      <DropdownMenu>
        <Tooltip label={t("recentAll.bulk.move")}>
          <DropdownMenuTrigger asChild>
            <Button variant="ghost" size="icon-sm" disabled={none} aria-label={t("recentAll.bulk.move")}>
              <FolderInput />
            </Button>
          </DropdownMenuTrigger>
        </Tooltip>
        <DropdownMenuContent className="max-h-72 overflow-y-auto">
          <DropdownMenuItem onSelect={() => onMove("")}>{t("recentAll.projectDefault")}</DropdownMenuItem>
          {projects.map((p) => (
            <DropdownMenuItem key={p.publicID} onSelect={() => onMove(p.publicID)}>
              <span className="truncate">{p.name}</span>
            </DropdownMenuItem>
          ))}
        </DropdownMenuContent>
      </DropdownMenu>
      {sharingEnabled ? (
        <Tooltip label={t("recentAll.bulk.stopSharing")}>
          <Button variant="ghost" size="icon-sm" disabled={sharedCount === 0} onClick={onStopSharing} aria-label={t("recentAll.bulk.stopSharing")}>
            <Link2Off />
          </Button>
        </Tooltip>
      ) : null}
      <Tooltip label={archiveAction === "archive" ? t("recentAll.bulk.archive") : t("recentAll.bulk.unarchive")}>
        <Button
          variant="ghost"
          size="icon-sm"
          disabled={none}
          onClick={onArchive}
          aria-label={archiveAction === "archive" ? t("recentAll.bulk.archive") : t("recentAll.bulk.unarchive")}
        >
          {archiveAction === "archive" ? <Archive /> : <ArchiveRestore />}
        </Button>
      </Tooltip>
      <Tooltip label={t("common.actions.delete")}>
        <Button variant="ghost" size="icon-sm" disabled={none} onClick={onDelete} aria-label={t("common.actions.delete")}>
          <Trash2 />
        </Button>
      </Tooltip>
      <Tooltip label={t("recentAll.bulk.cancel")}>
        <Button variant="ghost" size="icon-sm" onClick={onCancel} aria-label={t("recentAll.bulk.cancel")}>
          <X />
        </Button>
      </Tooltip>
    </div>
  );
}

export function IdleTools({ exporting, onSelect, onExport }: { exporting: boolean; onSelect: () => void; onExport: () => void }) {
  const t = useT();
  return (
    <div className="flex items-center gap-1">
      <Tooltip label={t("recentAll.bulk.selectMany")}>
        <Button variant="ghost" size="icon-sm" onClick={onSelect} aria-label={t("recentAll.bulk.selectMany")}>
          <CheckSquare />
        </Button>
      </Tooltip>
      <Tooltip label={t("recentAll.bulk.exportAll")}>
        <Button variant="ghost" size="icon-sm" onClick={onExport} disabled={exporting} loading={exporting} aria-label={t("recentAll.bulk.exportAll")}>
          {exporting ? null : <Download />}
        </Button>
      </Tooltip>
    </div>
  );
}

export function MasterCheckbox({ state, onToggle, visible }: { state: "none" | "some" | "all"; onToggle: () => void; visible: boolean }) {
  const t = useT();
  return (
    <Checkbox
      checked={state === "all"}
      indeterminate={state === "some"}
      onChange={onToggle}
      aria-label={t("recentAll.bulk.selectAll")}
      className={cn("max-md:hidden transition-opacity", visible ? "opacity-100" : "opacity-0 focus-visible:opacity-100 group-hover/toolbar:opacity-100")}
    />
  );
}
