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

export function KbSidebar(props: KbSidebarProps) {
  const { mode, list, collapsed, canCollapse, deleting, onToggleCollapsed, onOpen, onCreate, onEdit, onDelete, onBulkDelete } = props;
  const t = useT();
  const { state } = list;
  const [searchOpen, setSearchOpen] = useState(false);
  const showRail = collapsed && canCollapse;
  const title = mode === "admin" ? t("kbWs.titles.admin") : t("kbWs.titles.user");
  const selectable = selectableIDs(state.items, mode);
  const picked = state.checked.size;

  const openSearch = () => {
    if (showRail) onToggleCollapsed();
    setSearchOpen(true);
  };
  const closeSearch = () => {
    setSearchOpen(false);
    list.setQuery("");
  };

  const createButton = (
    <Tooltip label={t("kbWs.sidebar.create")}>
      <Button size="icon-sm" aria-label={t("kbWs.sidebar.create")} onClick={onCreate}>
        <Plus />
      </Button>
    </Tooltip>
  );

  if (showRail) {
    return (
      <aside className="flex w-12 shrink-0 flex-col items-center gap-2 border-r bg-card/50 py-3">
        <Tooltip label={t("kbWs.sidebar.expand")} side="right">
          <Button variant="ghost" size="icon-sm" aria-label={t("kbWs.sidebar.expand")} onClick={onToggleCollapsed}>
            <PanelLeftOpen />
          </Button>
        </Tooltip>
        <Tooltip label={t("kbWs.sidebar.search")} side="right">
          <Button variant="ghost" size="icon-sm" aria-label={t("kbWs.sidebar.search")} onClick={openSearch}>
            <Search />
          </Button>
        </Tooltip>
        {createButton}
      </aside>
    );
  }

  return (
    <aside className="flex min-h-0 w-full shrink-0 flex-col border-r bg-card/50 md:w-80" aria-label={t("kbWs.sidebar.listLabel")}>
      <div className="flex items-center gap-1 px-3 pb-2 pt-3">
        <h2 className="mr-auto flex min-w-0 items-center gap-2 text-sm font-semibold">
          <BookOpen aria-hidden className="size-4 shrink-0 text-muted-foreground" />
          <span className="truncate">{title}</span>
        </h2>
        <Tooltip label={searchOpen ? t("kbWs.sidebar.closeSearch") : t("kbWs.sidebar.search")}>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={searchOpen ? t("kbWs.sidebar.closeSearch") : t("kbWs.sidebar.search")}
            aria-pressed={searchOpen}
            onClick={() => (searchOpen ? closeSearch() : openSearch())}
          >
            {searchOpen ? <SearchX /> : <Search />}
          </Button>
        </Tooltip>
        {canCollapse ? (
          <Tooltip label={t("kbWs.sidebar.collapse")}>
            <Button variant="ghost" size="icon-sm" aria-label={t("kbWs.sidebar.collapse")} onClick={onToggleCollapsed}>
              <PanelLeftClose />
            </Button>
          </Tooltip>
        ) : null}
        {createButton}
      </div>

      {searchOpen ? (
        <div className="px-3 pb-2">
          <Input
            autoFocus
            value={list.query}
            placeholder={t("kbWs.sidebar.searchPlaceholder")}
            aria-label={t("kbWs.sidebar.search")}
            onChange={(e) => list.setQuery(e.target.value)}
            onKeyDown={(e) => e.key === "Escape" && closeSearch()}
          />
        </div>
      ) : null}

      <div className="flex items-center gap-1 px-3 pb-2">
        <Button
          variant="ghost"
          size="sm"
          className="-ml-2 text-muted-foreground"
          disabled={deleting || (picked === 0 && selectable.length === 0)}
          onClick={() => (picked > 0 ? list.setChecked([]) : list.setChecked(selectable))}
        >
          {picked > 0 ? t("kbWs.sidebar.clearSelection") : t("kbWs.sidebar.selectAll")}
        </Button>
        <div className="ml-auto flex items-center gap-1">
          {picked > 0 ? (
            <Button variant="destructive" size="sm" disabled={deleting} onClick={onBulkDelete}>
              <Trash2 />
              {t("kbWs.sidebar.deleteSelected")}
            </Button>
          ) : null}
          <DropdownMenu>
            <Tooltip label={t("kbWs.sidebar.sort")}>
              <DropdownMenuTrigger asChild>
                <Button variant={list.sort === "default" ? "ghost" : "secondary"} size="icon-sm" aria-label={t("kbWs.sidebar.sort")}>
                  <ArrowDownUp />
                </Button>
              </DropdownMenuTrigger>
            </Tooltip>
            <DropdownMenuContent>
              <DropdownMenuRadioGroup value={list.sort} onValueChange={(v) => list.setSort(v as (typeof KB_SORTS)[number])}>
                {KB_SORTS.map((key) => (
                  <DropdownMenuRadioItem key={key} value={key}>
                    {t(`kbWs.sorts.${key}`)}
                  </DropdownMenuRadioItem>
                ))}
              </DropdownMenuRadioGroup>
            </DropdownMenuContent>
          </DropdownMenu>
        </div>
      </div>

      <div className="min-h-0 flex-1 overflow-y-auto px-2 pb-3">
        {state.loading && state.items.length === 0 ? (
          <div className="grid gap-2 p-1" aria-busy role="status" aria-label={t("common.states.loading")}>
            {Array.from({ length: 5 }, (_, i) => (
              <Skeleton key={i} className="h-12" />
            ))}
          </div>
        ) : state.items.length === 0 && state.loadFailed ? (
          <EmptyState
            icon={<RotateCw />}
            title={t("kbWs.empty.loadFailedTitle")}
            description={t("kbWs.empty.loadFailedBody")}
            className="py-10"
            action={
              <Button variant="outline" size="sm" onClick={() => void list.load()}>
                {t("common.actions.retry")}
              </Button>
            }
          />
        ) : state.items.length === 0 ? (
          <EmptyState
            icon={<BookOpen />}
            title={list.query.trim() ? t("kbWs.empty.searchTitle") : t("kbWs.empty.noneTitle")}
            description={list.query.trim() ? t("kbWs.empty.searchBody") : t("kbWs.empty.noneBody")}
            className="py-10"
          />
        ) : (
          <ul className="grid gap-0.5">
            {state.items.map((kb) => (
              <KbRow
                key={kb.publicID}
                kb={kb}
                mode={mode}
                active={state.selectedId === kb.publicID}
                checked={state.checked.has(kb.publicID)}
                onOpen={() => onOpen(kb.publicID)}
                onToggle={() => list.toggleChecked(kb.publicID)}
                onEdit={() => onEdit(kb)}
                onDelete={() => onDelete(kb)}
              />
            ))}
          </ul>
        )}
        {state.items.length < state.total ? (
          <div className="flex justify-center pt-2">
            <Button variant="ghost" size="sm" disabled={state.loading || state.loadingMore} onClick={() => void list.loadMore()}>
              {state.loadingMore ? <Spinner /> : null}
              {t("kbWs.sidebar.loadMore")}
            </Button>
          </div>
        ) : null}
      </div>
    </aside>
  );
}
