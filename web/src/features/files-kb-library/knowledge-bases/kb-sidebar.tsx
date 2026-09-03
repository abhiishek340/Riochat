"use client";

import { useState } from "react";
import { ArrowDownUp, BookOpen, MoreHorizontal, PanelLeftClose, PanelLeftOpen, Plus, RotateCw, Search, SearchX, Trash2 } from "lucide-react";
import {
  Badge,
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
  EmptyState,
  Input,
  Skeleton,
  Spinner,
  Tooltip,
} from "@/components/ui";
import { useT, type Translate } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import type { KnowledgeBase } from "../shared/types";
import { Checkbox } from "../shared/ui/checkbox";
import { KB_SORTS, type KbMode } from "./api";
import { healthOf, isEditable, selectableIDs, type Health } from "./model";
import type { KbList } from "./use-kb-list";

export function healthText(t: Translate, health: Health): string {
  switch (health.kind) {
    case "none":
      return t("kbWs.health.none");
    case "all":
      return t("kbWs.health.all", { count: health.count });
    case "some":
      return t("kbWs.health.some", { ready: health.ready, total: health.total });
    default:
      return t("kbWs.health.notReady", { count: health.count });
  }
}

function KbRow({
  kb,
  mode,
  active,
  checked,
  onOpen,
  onToggle,
  onEdit,
  onDelete,
}: {
  kb: KnowledgeBase;
  mode: KbMode;
  active: boolean;
  checked: boolean;
  onOpen: () => void;
  onToggle: () => void;
  onEdit: () => void;
  onDelete: () => void;
}) {
  const t = useT();
  const editable = isEditable(kb, mode);
  const [menuOpen, setMenuOpen] = useState(false);
  return (
    <li
      data-active={active || undefined}
      className={cn("group relative flex items-start gap-2 rounded-lg px-2 py-2 transition-colors hover:bg-accent/60", active && "bg-accent")}
    >
      <Checkbox
        className="mt-0.5"
        checked={checked}
        disabled={!editable}
        onCheckedChange={onToggle}
        aria-label={t("kbWs.row.checkbox", { name: kb.name })}
      />
      <button
        type="button"
        onClick={onOpen}
        aria-current={active || undefined}
        className="min-w-0 flex-1 rounded-md text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <span className="flex items-center gap-1.5">
          <span className={cn("truncate text-sm font-medium", mode === "admin" && !kb.enabled && "text-muted-foreground")} title={kb.name}>
            {kb.name}
          </span>
          {kb.scope === "builtin" ? <Badge tone="primary">{t("kbWs.badges.builtin")}</Badge> : null}
          {mode === "admin" && !kb.enabled ? <Badge tone="outline">{t("kbWs.badges.disabled")}</Badge> : null}
        </span>
        <span className="mt-0.5 block truncate text-xs text-muted-foreground">{healthText(t, healthOf(kb))}</span>
      </button>
      {editable ? (
        <div
          className={cn(
            "flex shrink-0 items-center gap-0.5 transition-opacity md:opacity-0 md:group-focus-within:opacity-100 md:group-hover:opacity-100",
            (menuOpen || active) && "md:opacity-100",
          )}
        >
          <DropdownMenu open={menuOpen} onOpenChange={setMenuOpen}>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-xs" aria-label={t("kbWs.row.moreNamed", { name: kb.name })}>
                <MoreHorizontal />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem onSelect={onToggle}>{checked ? t("kbWs.row.deselect") : t("kbWs.row.select")}</DropdownMenuItem>
              <DropdownMenuItem onSelect={onEdit}>{t("kbWs.row.edit")}</DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
          <Tooltip label={t("kbWs.row.delete")}>
            <Button variant="ghost" size="icon-xs" aria-label={t("kbWs.row.deleteNamed", { name: kb.name })} onClick={onDelete} className="hover:text-destructive">
              <Trash2 />
            </Button>
          </Tooltip>
        </div>
      ) : null}
    </li>
  );
}

export interface KbSidebarProps {
  mode: KbMode;
  list: KbList;
  collapsed: boolean;
  canCollapse: boolean;
  deleting: boolean;
  onToggleCollapsed: () => void;
  onOpen: (id: string) => void;
  onCreate: () => void;
  onEdit: (kb: KnowledgeBase) => void;
  onDelete: (kb: KnowledgeBase) => void;
  onBulkDelete: () => void;
}
