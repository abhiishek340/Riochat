"use client";

import Link from "next/link";
import { Archive, ArchiveRestore, CheckSquare, Download, FolderInput, MoreHorizontal, Pencil, Share2, Star, StarOff, Tag, Trash2, Link2 } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { FeatureGate } from "@/lib/capabilities/provider";
import { useI18n } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { Checkbox } from "./checkbox";
import { formatRelative, isArchived, isShared, type ConversationRow } from "./model";
import type { ProjectRef } from "./api";

export interface RowActions {
  toggleSelect: (row: ConversationRow) => void;
  star: (row: ConversationRow) => void;
  rename: (row: ConversationRow) => void;
  labels: (row: ConversationRow) => void;
  move: (row: ConversationRow, projectId: string) => void;
  share: (row: ConversationRow) => void;
  exportJson: (row: ConversationRow) => void;
  archive: (row: ConversationRow) => void;
  remove: (row: ConversationRow) => void;
}

const MAX_BADGES = 3;

export function ConversationItem({
  row,
  now,
  selected,
  selectionMode,
  projects,
  actions,
  mergeTop,
  mergeBottom,
}: {
  row: ConversationRow;
  now: Date;
  selected: boolean;
  selectionMode: boolean;
  projects: ProjectRef[];
  actions: RowActions;
  mergeTop: boolean;
  mergeBottom: boolean;
}) {
  const { t, locale } = useI18n();
  const title = row.title.trim() || t("recentAll.untitled");
  const when = formatRelative(row.updatedAt, now, locale);
  const shown = row.labels.length > MAX_BADGES ? row.labels.slice(0, MAX_BADGES - 1) : row.labels;
  const extra = row.labels.length - shown.length;
  const href = `/chat?conversation_id=${encodeURIComponent(row.publicID)}`;

  const body = (
    <>
      <span className="min-w-0 flex-1">
        <span className="flex items-center gap-2">
          <span className="truncate text-sm font-medium">{title}</span>
          {isArchived(row) ? <Archive className="size-3.5 shrink-0 text-muted-foreground" aria-label={t("recentAll.status.archived")} /> : null}
          {row.isStarred ? <Star className="size-3.5 shrink-0 fill-warning text-warning" aria-label={t("recentAll.status.starred")} /> : null}
          {isShared(row) ? <Link2 className="size-3.5 shrink-0 text-primary" aria-label={t("recentAll.status.shared")} /> : null}
        </span>
        <span className="mt-0.5 flex flex-wrap items-center gap-1.5 text-xs text-muted-foreground">
          <span>{when ? t("recentAll.updated", { when }) : t("recentAll.updatedJustNow")}</span>
          {shown.map((label) => (
            <Badge key={label} tone="outline" className="max-w-28 truncate">
              {label}
            </Badge>
          ))}
          {extra > 0 ? <Badge tone="outline">+{extra}</Badge> : null}
        </span>
      </span>
    </>
  );

  return (
    <li
      data-selected={selected || undefined}
      className={cn(
        "group relative flex items-center gap-2 border border-transparent px-2 py-2.5 transition-colors",
        selected ? "bg-accent" : "hover:bg-muted/70",
        mergeTop && selected ? "rounded-t-none" : "rounded-t-xl",
        mergeBottom && selected ? "rounded-b-none" : "rounded-b-xl",
      )}
    >
      <Checkbox
        checked={selected}
        onChange={() => actions.toggleSelect(row)}
        aria-label={t("recentAll.select", { title })}
        className={cn("transition-opacity", selectionMode ? "opacity-100" : "opacity-0 focus-visible:opacity-100 group-hover:opacity-100 max-md:hidden")}
      />
      {selectionMode ? (
        <button type="button" onClick={() => actions.toggleSelect(row)} className="flex min-w-0 flex-1 items-center gap-2 text-left outline-none focus-visible:ring-2 focus-visible:ring-ring">
          {body}
        </button>
      ) : (
        <Link href={href} prefetch={false} className="flex min-w-0 flex-1 items-center gap-2 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring">
          {body}
        </Link>
      )}
      <RowMenu row={row} selected={selected} projects={projects} actions={actions} />
    </li>
  );
}

function RowMenu({ row, selected, projects, actions }: { row: ConversationRow; selected: boolean; projects: ProjectRef[]; actions: RowActions }) {
  const { t } = useI18n();
  const archived = isArchived(row);
  return (
    <DropdownMenu>
      <DropdownMenuTrigger asChild>
        <Button variant="ghost" size="icon-sm" aria-label={t("recentAll.menu.open")} className="shrink-0 opacity-70 group-hover:opacity-100 data-[state=open]:opacity-100">
          <MoreHorizontal />
        </Button>
      </DropdownMenuTrigger>
      <DropdownMenuContent className="w-56">
        <DropdownMenuItem onSelect={() => actions.toggleSelect(row)}>
          <CheckSquare />
          {selected ? t("recentAll.menu.deselect") : t("recentAll.menu.select")}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => actions.star(row)}>
          {row.isStarred ? <StarOff /> : <Star />}
          {row.isStarred ? t("recentAll.menu.unstar") : t("recentAll.menu.star")}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => actions.rename(row)}>
          <Pencil />
          {t("recentAll.menu.rename")}
        </DropdownMenuItem>
        <DropdownMenuItem onSelect={() => actions.labels(row)}>
          <Tag />
          {t("recentAll.menu.labels")}
        </DropdownMenuItem>
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <FolderInput />
            {t("recentAll.menu.move")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent className="max-h-72 overflow-y-auto">
            <DropdownMenuItem onSelect={() => actions.move(row, "")}>
              {t("recentAll.projectDefault")}
              {row.projectID === "" ? <span className="ml-auto text-xs text-muted-foreground">{t("recentAll.menu.current")}</span> : null}
            </DropdownMenuItem>
            {projects.map((p) => (
              <DropdownMenuItem key={p.publicID} onSelect={() => actions.move(row, p.publicID)}>
                <span className="truncate">{p.name}</span>
                {row.projectID === p.publicID ? <span className="ml-auto text-xs text-muted-foreground">{t("recentAll.menu.current")}</span> : null}
              </DropdownMenuItem>
            ))}
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        <DropdownMenuSub>
          <DropdownMenuSubTrigger>
            <Share2 />
            {t("recentAll.menu.shareExport")}
          </DropdownMenuSubTrigger>
          <DropdownMenuSubContent>
            <FeatureGate feature="sharing">
              <DropdownMenuItem onSelect={() => actions.share(row)}>
                <Link2 />
                {isShared(row) ? t("recentAll.menu.manageShare") : t("recentAll.menu.share")}
              </DropdownMenuItem>
            </FeatureGate>
            <DropdownMenuItem onSelect={() => actions.exportJson(row)}>
              <Download />
              {t("recentAll.menu.exportJson")}
            </DropdownMenuItem>
          </DropdownMenuSubContent>
        </DropdownMenuSub>
        <DropdownMenuItem onSelect={() => actions.archive(row)}>
          {archived ? <ArchiveRestore /> : <Archive />}
          {archived ? t("recentAll.menu.unarchive") : t("recentAll.menu.archive")}
        </DropdownMenuItem>
        <DropdownMenuSeparator />
        <DropdownMenuItem destructive onSelect={() => actions.remove(row)}>
          <Trash2 />
          {t("common.actions.delete")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
