"use client";

import { useEffect, useRef, useState } from "react";
import {
  ArrowDownUp,
  Filter,
  FolderOpen,
  PanelLeftClose,
  PanelLeftOpen,
  RotateCw,
  Search,
  SearchX,
  Sparkles,
  Trash2,
  Upload,
  X,
} from "lucide-react";
import {
  Button,
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuRadioGroup,
  DropdownMenuRadioItem,
  DropdownMenuTrigger,
  EmptyState,
  Input,
  Skeleton,
  Tooltip,
} from "@/components/ui";
import { DropdownMenuCheckboxItem } from "@/components/ui/dropdown-menu-checkbox-item";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { FILTER_KEYS, SORT_KEYS } from "../shared/file-kind";
import type { FileActions } from "./use-file-actions";
import type { FileLibrary } from "./use-file-library";
import type { FileObject } from "../shared/types";
import { FileRow } from "./file-row";
import { QuotaPanel } from "./quota-panel";

const NEAR_BOTTOM_PX = 200;

export interface FileSidebarProps {
  lib: FileLibrary;
  actions: FileActions;
  collapsed: boolean;
  /** Mobile never shows the narrow rail. */
  canCollapse: boolean;
  onToggleCollapsed: () => void;
  onOpenFile: (id: string) => void;
  onRequestDelete: (file: FileObject) => void;
  onRequestBulkDelete: () => void;
}

export function FileSidebar(props: FileSidebarProps) {
  const { lib, actions, collapsed, canCollapse, onToggleCollapsed, onOpenFile, onRequestDelete, onRequestBulkDelete } = props;
  const t = useT();
  const { state } = lib;
  const [searchOpen, setSearchOpen] = useState(false);
  const picker = useRef<HTMLInputElement>(null);
  const scroller = useRef<HTMLDivElement>(null);

  const showRail = collapsed && canCollapse;
  const loadingFirst = state.phase === "loading";
  // Nothing to show yet and a request is on its way (first load, or a new search/filter/sort).
  const waitingForList = state.items.length === 0 && (state.phase === "loading" || state.phase === "syncing");
  const selectedCount = state.checked.size;
  const anyVectorizable = state.items.some((f) => state.checked.has(f.fileID) && f.canVectorize);

  const openSearch = () => {
    if (showRail) onToggleCollapsed();
    setSearchOpen(true);
  };
  const closeSearch = () => {
    setSearchOpen(false);
    lib.setQuery("");
  };

  const maybeLoadMore = () => {
    const el = scroller.current;
    if (el && el.scrollHeight - el.scrollTop - el.clientHeight < NEAR_BOTTOM_PX) lib.loadMore();
  };

  // A short first page may not overflow, so nothing would ever scroll: top it up.
  const { items, hasMore, phase } = state;
  useEffect(() => {
    const el = scroller.current;
    if (!el || !hasMore || phase !== "idle") return;
    if (el.scrollHeight - el.clientHeight < NEAR_BOTTOM_PX) lib.loadMore();
  }, [items.length, hasMore, phase, lib]);

  const onPick = (event: React.ChangeEvent<HTMLInputElement>) => {
    const files = Array.from(event.target.files ?? []);
    event.target.value = "";
    void actions.upload(files);
  };

  const uploadButton = (
    <Tooltip label={lib.uploading ? t("filesWs.sidebar.uploading") : t("filesWs.sidebar.upload")}>
      <Button
        variant="default"
        size="icon-sm"
        aria-label={t("filesWs.sidebar.upload")}
        loading={lib.uploading}
        onClick={() => picker.current?.click()}
      >
        <Upload />
      </Button>
    </Tooltip>
  );
  const hiddenPicker = <input ref={picker} type="file" multiple hidden onChange={onPick} data-testid="file-picker" />;

  if (showRail) {
    return (
      <aside className="flex w-12 shrink-0 flex-col items-center gap-2 border-r bg-card/50 py-3">
        <Tooltip label={t("filesWs.sidebar.expand")} side="right">
          <Button variant="ghost" size="icon-sm" aria-label={t("filesWs.sidebar.expand")} onClick={onToggleCollapsed}>
            <PanelLeftOpen />
          </Button>
        </Tooltip>
        <Tooltip label={t("filesWs.sidebar.search")} side="right">
          <Button variant="ghost" size="icon-sm" aria-label={t("filesWs.sidebar.search")} onClick={openSearch}>
            <Search />
          </Button>
        </Tooltip>
        {uploadButton}
        {hiddenPicker}
      </aside>
    );
  }

  const footer = state.phase === "more"
    ? t("filesWs.footer.loadingMore")
    : state.phase === "syncing"
      ? t("filesWs.footer.syncing")
      : state.hasMore
        ? t("filesWs.footer.scrollForMore")
        : state.items.length > 0
          ? t("filesWs.footer.allLoaded")
          : "";

  return (
    <aside className="flex min-h-0 w-full shrink-0 flex-col border-r bg-card/50 md:w-72" aria-label={t("filesWs.sidebar.listLabel")}>
      <div className="flex items-center gap-1 px-3 pb-2 pt-3">
        <h2 className="mr-auto flex items-center gap-2 text-sm font-semibold">
          <FolderOpen aria-hidden className="size-4 text-muted-foreground" />
          {t("shell.nav.files")}
        </h2>
        <Tooltip label={searchOpen ? t("filesWs.sidebar.closeSearch") : t("filesWs.sidebar.search")}>
          <Button
            variant="ghost"
            size="icon-sm"
            aria-label={searchOpen ? t("filesWs.sidebar.closeSearch") : t("filesWs.sidebar.search")}
            aria-pressed={searchOpen}
            onClick={() => (searchOpen ? closeSearch() : openSearch())}
          >
            {searchOpen ? <SearchX /> : <Search />}
          </Button>
        </Tooltip>
        {canCollapse ? (
          <Tooltip label={t("filesWs.sidebar.collapse")}>
            <Button variant="ghost" size="icon-sm" aria-label={t("filesWs.sidebar.collapse")} onClick={onToggleCollapsed}>
              <PanelLeftClose />
            </Button>
          </Tooltip>
        ) : null}
        {uploadButton}
        {hiddenPicker}
      </div>

      {searchOpen ? (
        <div className="relative px-3 pb-2">
          <Input
            autoFocus
            value={lib.query}
            placeholder={t("filesWs.sidebar.searchPlaceholder")}
            aria-label={t("filesWs.sidebar.search")}
            onChange={(e) => lib.setQuery(e.target.value)}
            onKeyDown={(e) => e.key === "Escape" && closeSearch()}
          />
        </div>
      ) : null}

      <div className="flex items-center gap-1 px-3 pb-2">
        <Button
          variant="ghost"
          size="sm"
          className="-ml-2 text-muted-foreground"
          disabled={actions.bulkDeleting || (selectedCount === 0 && (loadingFirst || state.items.length === 0))}
          onClick={() => (selectedCount > 0 ? lib.clearChecked() : lib.selectAll())}
        >
          {selectedCount > 0 ? t("filesWs.sidebar.clearSelection") : t("filesWs.sidebar.selectAll")}
        </Button>
        <div className="ml-auto flex items-center gap-1">
          {selectedCount > 0 ? (
            <>
              <Button
                variant="outline"
                size="sm"
                disabled={!anyVectorizable || actions.vectorizeBusy}
                onClick={() => void actions.vectorize(state.items.filter((f) => state.checked.has(f.fileID) && f.canVectorize).map((f) => f.fileID))}
              >
                <Sparkles />
                {t("filesWs.sidebar.buildIndexSelected")}
              </Button>
              <Button variant="destructive" size="sm" disabled={actions.bulkDeleting} onClick={onRequestBulkDelete}>
                <Trash2 />
                {t("filesWs.sidebar.deleteSelected")}
              </Button>
            </>
          ) : (
            <>
              <DropdownMenu>
                <Tooltip label={t("filesWs.sidebar.filter")}>
                  <DropdownMenuTrigger asChild>
                    <Button
                      variant={lib.filters.length > 0 ? "secondary" : "ghost"}
                      size="icon-sm"
                      aria-label={t("filesWs.sidebar.filter")}
                      className={cn(lib.filters.length > 0 && "text-primary")}
                    >
                      <Filter />
                    </Button>
                  </DropdownMenuTrigger>
                </Tooltip>
                <DropdownMenuContent>
                  <DropdownMenuCheckboxItem checked={lib.filters.length === 0} onSelect={(e) => e.preventDefault()} onCheckedChange={() => lib.clearKinds()}>
                    {t("filesWs.sidebar.filterAll")}
                  </DropdownMenuCheckboxItem>
                  {FILTER_KEYS.map((key) => (
                    <DropdownMenuCheckboxItem
                      key={key}
                      checked={lib.filters.includes(key)}
                      onSelect={(e) => e.preventDefault()}
                      onCheckedChange={() => lib.toggleKind(key)}
                    >
                      {t(`filesWs.filters.${key}`)}
                    </DropdownMenuCheckboxItem>
                  ))}
                </DropdownMenuContent>
              </DropdownMenu>
              <DropdownMenu>
                <Tooltip label={t("filesWs.sidebar.sort")}>
                  <DropdownMenuTrigger asChild>
                    <Button variant="ghost" size="icon-sm" aria-label={t("filesWs.sidebar.sort")}>
                      <ArrowDownUp />
                    </Button>
                  </DropdownMenuTrigger>
                </Tooltip>
                <DropdownMenuContent>
                  <DropdownMenuRadioGroup value={lib.sort} onValueChange={lib.setSort}>
                    {SORT_KEYS.map((key) => (
                      <DropdownMenuRadioItem key={key} value={key}>
                        {t(`filesWs.sorts.${key}`)}
                      </DropdownMenuRadioItem>
                    ))}
                  </DropdownMenuRadioGroup>
                </DropdownMenuContent>
              </DropdownMenu>
            </>
          )}
        </div>
      </div>

      <div ref={scroller} onScroll={maybeLoadMore} className="min-h-0 flex-1 overflow-y-auto px-2">
        {waitingForList ? (
          <div className="grid gap-2 p-1" aria-busy role="status" aria-label={t("common.states.loading")}>
            {Array.from({ length: 7 }, (_, i) => (
              <Skeleton key={i} className="h-8" />
            ))}
          </div>
        ) : state.items.length === 0 && state.loadFailed ? (
          <EmptyState
            icon={<RotateCw />}
            title={t("filesWs.empty.loadFailedTitle")}
            description={t("filesWs.empty.loadFailedBody")}
            className="py-10"
            action={
              <Button variant="outline" size="sm" onClick={lib.retry}>
                {t("common.actions.retry")}
              </Button>
            }
          />
        ) : state.items.length === 0 ? (
          <EmptyState
            icon={lib.hasActiveQuery ? <X /> : <FolderOpen />}
            title={lib.hasActiveQuery ? t("filesWs.empty.noMatchTitle") : t("filesWs.empty.noneTitle")}
            description={lib.hasActiveQuery ? t("filesWs.empty.noMatchBody") : t("filesWs.empty.noneBody")}
            className="py-10"
          />
        ) : (
          <ul className="grid gap-0.5 pb-2">
            {state.items.map((file) => (
              <FileRow
                key={file.fileID}
                file={file}
                active={state.selectedId === file.fileID}
                checked={state.checked.has(file.fileID)}
                submitting={actions.submittingIds.has(file.fileID)}
                vectorizeBusy={actions.vectorizeBusy}
                onOpen={() => onOpenFile(file.fileID)}
                onToggleChecked={() => lib.toggleChecked(file.fileID)}
                onDelete={() => onRequestDelete(file)}
                onVectorize={() => void actions.vectorize([file.fileID])}
                onRename={(name) => actions.rename(file.fileID, name)}
              />
            ))}
          </ul>
        )}
        {state.moreFailed || (state.loadFailed && state.items.length > 0) ? (
          <div className="flex items-center justify-center gap-2 px-2 pb-3 pt-1 text-xs text-destructive" role="alert">
            <span>{t("filesWs.footer.moreFailed")}</span>
            <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => (state.moreFailed ? lib.loadMore(true) : lib.retry())}>
              {t("common.actions.retry")}
            </Button>
          </div>
        ) : footer ? (
          <p className="px-2 pb-3 pt-1 text-center text-xs text-muted-foreground" role="status">
            {footer}
          </p>
        ) : null}
      </div>
      {state.quota ? <QuotaPanel quota={state.quota} /> : null}
    </aside>
  );
}
