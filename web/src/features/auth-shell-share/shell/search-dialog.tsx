"use client";

import { useEffect, useId, useMemo, useRef, useState } from "react";
import { useRouter } from "next/navigation";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { Archive, ArrowRight, CornerDownLeft, FolderClosed, PanelRight, Search, Star } from "lucide-react";
import { Button } from "@/components/ui/button";
import { DialogOverlay, DialogPortal } from "@/components/ui/dialog";
import { Spinner } from "@/components/ui/spinner";
import { useStoredBoolean } from "@/hooks/use-stored-boolean";
import { useMediaQuery } from "@/hooks/use-media-query";
import { useI18n } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { formatUpdatedLabel, groupByUpdated, stepIndex, type SearchGroup, type SearchItem } from "../lib/search-model";
import { SearchPreview } from "./search-preview";
import { MAX_QUERY_LENGTH, useConversationSearch } from "./use-conversation-search";

export const PREVIEW_STORAGE_KEY = "riochat.navigation-search.preview.open";

function groupLabel(group: SearchGroup, locale: string, now: Date, today: string): string {
  if (group.key.kind === "today") return today;
  const date = new Date(group.key.year, group.key.month, 1);
  return new Intl.DateTimeFormat(locale, group.key.year === now.getFullYear() ? { month: "long" } : { year: "numeric", month: "long" }).format(date);
}

/** Global conversation search (command palette). Results come from the server; the list is never filtered locally. */
export function ConversationSearchDialog({ open, onOpenChange }: { open: boolean; onOpenChange: (open: boolean) => void }) {
  const { t, locale } = useI18n();
  const router = useRouter();
  const search = useConversationSearch(open);
  const [active, setActive] = useState(-1);
  const [hovered, setHovered] = useState<string | null>(null);
  const [previewOpen, setPreviewOpen] = useStoredBoolean(PREVIEW_STORAGE_KEY, true);
  const wide = useMediaQuery("(min-width: 1024px) and (hover: hover) and (pointer: fine)");
  const listId = useId();
  const sentinel = useRef<HTMLDivElement>(null);
  const now = useMemo(() => new Date(), [open]); // eslint-disable-line react-hooks/exhaustive-deps -- fresh clock each time the dialog opens

  const groups = useMemo(() => groupByUpdated(search.items, now), [search.items, now]);
  const flat = useMemo(() => groups.flatMap((g) => g.items), [groups]);

  useEffect(() => {
    setActive(-1);
    setHovered(null);
  }, [search.query, open]);

  // Infinite scroll: load the next page when the sentinel scrolls near the viewport.
  const { loadMore } = search;
  useEffect(() => {
    const el = sentinel.current;
    if (!open || !el || typeof IntersectionObserver === "undefined") return;
    const observer = new IntersectionObserver((entries) => entries.some((e) => e.isIntersecting) && loadMore(), { rootMargin: "120px" });
    observer.observe(el);
    return () => observer.disconnect();
  }, [open, loadMore, flat.length]);

  useEffect(() => {
    if (active < 0) return;
    document.getElementById(`${listId}-${active}`)?.scrollIntoView?.({ block: "nearest" });
  }, [active, listId]);

  const choose = (item: SearchItem) => {
    onOpenChange(false);
    router.push(`/chat?conversation_id=${encodeURIComponent(item.publicID)}`);
  };

  const onKeyDown = (event: React.KeyboardEvent) => {
    if (event.nativeEvent.isComposing) return;
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      event.preventDefault();
      // Keyboard movement takes over from a pointer that was resting on an older row.
      setHovered(null);
      setActive((current) => stepIndex(current, event.key === "ArrowDown" ? 1 : -1, flat.length));
    } else if (event.key === "Enter") {
      const item = flat[active] ?? (flat.length === 1 ? flat[0] : undefined);
      if (item) {
        event.preventDefault();
        choose(item);
      }
    }
  };

  const focusedId = active >= 0 ? flat[active]?.publicID ?? null : null;
  const previewId = wide && previewOpen ? (hovered ?? focusedId) : null;
  const showPreview = wide && previewOpen;
  const empty = !search.loading && !search.error && flat.length === 0;

  return (
    <DialogPrimitive.Root open={open} onOpenChange={onOpenChange}>
      <DialogPortal>
        <DialogOverlay />
        <DialogPrimitive.Content
          aria-describedby={undefined}
          className={cn(
            "fixed left-1/2 top-[8dvh] z-50 flex max-h-[min(36rem,84dvh)] w-[calc(100vw-1.5rem)] -translate-x-1/2 flex-col overflow-hidden rounded-2xl border bg-popover text-popover-foreground shadow-lg outline-none",
            showPreview ? "max-w-5xl" : "max-w-xl",
          )}
        >
          <DialogPrimitive.Title className="sr-only">{t("shellX.search.title")}</DialogPrimitive.Title>
          <div className="flex items-center gap-2 border-b px-4">
            <Search className="size-4 shrink-0 text-muted-foreground" />
            <input
              autoFocus
              role="combobox"
              aria-expanded={flat.length > 0}
              aria-autocomplete="list"
              aria-controls={listId}
              aria-activedescendant={active >= 0 ? `${listId}-${active}` : undefined}
              aria-label={t("shellX.search.title")}
              placeholder={t("shellX.search.placeholder")}
              maxLength={MAX_QUERY_LENGTH}
              value={search.query}
              onChange={(e) => search.setQuery(e.target.value)}
              onKeyDown={onKeyDown}
              className="h-12 min-w-0 flex-1 bg-transparent text-[15px] outline-none placeholder:text-muted-foreground"
            />
            {search.loading ? <Spinner className="text-muted-foreground" /> : null}
          </div>

          <div className="flex min-h-0 flex-1">
            <div className={cn("min-h-0 flex-1 overflow-y-auto p-2", showPreview && "max-w-[26rem] border-r")}>
              <ul id={listId} role="listbox" aria-busy={search.loading} aria-label={t("shellX.search.results")} className="grid gap-0.5">
                {groups.map((group) => {
                  const heading = groupLabel(group, locale, now, t("shellX.search.today"));
                  return (
                    <li key={group.id} role="presentation" className="grid gap-0.5">
                      <p className="px-2.5 pb-1 pt-3 text-xs font-semibold text-muted-foreground first:pt-1">{heading}</p>
                      <ul role="group" aria-label={heading} className="grid gap-0.5">
                        {group.items.map((item) => {
                          const index = flat.indexOf(item);
                          return (
                            <ResultRow
                              key={item.publicID}
                              id={`${listId}-${index}`}
                              item={item}
                              active={index === active}
                              now={now}
                              locale={locale}
                              onSelect={() => choose(item)}
                              onHover={() => {
                                setActive(index);
                                setHovered(item.publicID);
                              }}
                            />
                          );
                        })}
                      </ul>
                    </li>
                  );
                })}
              </ul>

              {empty ? (
                <p role="status" className="px-3 py-10 text-center text-sm text-muted-foreground">
                  {t("shellX.search.empty")}
                </p>
              ) : null}
              {search.error ? (
                <div role="alert" className="grid justify-items-center gap-2 px-3 py-10 text-center text-sm text-muted-foreground">
                  <p>{t("shellX.search.failed")}</p>
                  <Button size="sm" variant="outline" onClick={search.retryFirst}>
                    {t("common.actions.retry")}
                  </Button>
                </div>
              ) : null}
              {search.moreError ? (
                <div role="alert" className="flex items-center justify-between gap-2 px-3 py-2 text-sm text-muted-foreground">
                  <span>{t("shellX.search.moreFailed")}</span>
                  <Button size="sm" variant="ghost" onClick={search.retryMore}>
                    {t("common.actions.retry")}
                  </Button>
                </div>
              ) : null}
              {search.loadingMore ? (
                <div role="status" className="flex justify-center py-3">
                  <Spinner className="text-muted-foreground" />
                </div>
              ) : null}
              <div ref={sentinel} aria-hidden className="h-px" />
            </div>

            {showPreview ? (
              <div className="hidden min-w-0 flex-1 lg:block">
                <SearchPreview conversationId={previewId} />
              </div>
            ) : null}
          </div>

          <div className="flex items-center justify-between gap-3 border-t px-4 py-2 text-xs text-muted-foreground">
            <span className="flex items-center gap-3">
              <span className="flex items-center gap-1">
                <kbd className="rounded border bg-muted px-1">↑</kbd>
                <kbd className="rounded border bg-muted px-1">↓</kbd>
                {t("shellX.search.navigate")}
              </span>
              <span className="flex items-center gap-1">
                <CornerDownLeft className="size-3" />
                {t("shellX.search.open")}
              </span>
            </span>
            {wide ? (
              <button
                type="button"
                onClick={() => setPreviewOpen(!previewOpen)}
                aria-pressed={previewOpen}
                className="flex items-center gap-1.5 rounded-md px-2 py-1 outline-none hover:bg-accent focus-visible:ring-2 focus-visible:ring-ring"
              >
                <PanelRight className="size-3.5" />
                {previewOpen ? t("shellX.search.hidePreview") : t("shellX.search.showPreview")}
              </button>
            ) : null}
          </div>
        </DialogPrimitive.Content>
      </DialogPortal>
    </DialogPrimitive.Root>
  );
}
