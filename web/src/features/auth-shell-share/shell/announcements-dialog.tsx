"use client";

import { useEffect, useMemo, useState } from "react";
import { Megaphone, Pin } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { MiniMarkdown } from "../lib/mini-markdown";
import {
  formatAnnouncementDate,
  formatClock,
  isUnread,
  sortAnnouncements,
  type Announcement,
  type AnnouncementSort,
  type AnnouncementType,
} from "../lib/announcements-model";

const BAR: Record<AnnouncementType, string> = {
  critical: "bg-destructive",
  warning: "bg-warning",
  info: "bg-info",
  normal: "bg-success",
  general: "bg-muted-foreground/40",
};

const SORTS: AnnouncementSort[] = ["default", "type", "time"];

export function AnnouncementsDialog({
  open,
  mode,
  items,
  loading,
  saving,
  sort,
  onSortChange,
  onHide,
  onDismissToday,
  onClose,
}: {
  open: boolean;
  mode: "auto" | "manual";
  items: Announcement[];
  loading: boolean;
  saving: boolean;
  sort: AnnouncementSort;
  onSortChange: (sort: AnnouncementSort) => void;
  onHide: () => void;
  onDismissToday: () => void;
  onClose: () => void;
}) {
  const t = useT();
  const sorted = useMemo(() => sortAnnouncements(items, sort), [items, sort]);
  const [selectedId, setSelectedId] = useState<number | null>(null);

  // A new sort order or a fresh list starts at the first entry.
  useEffect(() => setSelectedId(null), [sort, items, open]);
  const selected = sorted.find((i) => i.id === selectedId) ?? sorted[0] ?? null;

  return (
    <Dialog open={open} onOpenChange={(next) => !next && !saving && onHide()}>
      <DialogContent className="max-w-3xl gap-0 p-0" aria-describedby={undefined}>
        <DialogHeader className="border-b px-6 py-4">
          <DialogTitle className="flex items-center gap-2">
            <Megaphone className="size-5 text-primary" />
            {t("announcements.title")}
          </DialogTitle>
          <DialogDescription className="sr-only">{t("announcements.description")}</DialogDescription>
        </DialogHeader>

        <div className="grid min-h-0 md:grid-cols-[16rem_1fr]">
          <div className="flex min-h-0 flex-col border-b md:border-b-0 md:border-r">
            <div role="group" aria-label={t("announcements.sortLabel")} className="grid grid-cols-3 gap-1 p-2">
              {SORTS.map((key) => (
                <button
                  key={key}
                  type="button"
                  aria-pressed={sort === key}
                  onClick={() => onSortChange(key)}
                  className={cn(
                    "rounded-md px-2 py-1 text-xs font-medium outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring",
                    sort === key ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:bg-muted",
                  )}
                >
                  {t(`announcements.sort.${key}`)}
                </button>
              ))}
            </div>
            <ul className="max-h-48 min-h-0 overflow-y-auto p-2 pt-0 md:max-h-[22rem]" aria-label={t("announcements.listLabel")}>
              {sorted.map((item) => (
                <li key={item.id}>
                  <button
                    type="button"
                    onClick={() => setSelectedId(item.id)}
                    aria-current={selected?.id === item.id}
                    className={cn(
                      "flex w-full items-stretch gap-2.5 rounded-lg px-2 py-2 text-left outline-none transition-colors focus-visible:ring-2 focus-visible:ring-ring",
                      selected?.id === item.id ? "bg-accent" : "hover:bg-muted",
                    )}
                  >
                    <span aria-hidden className={cn("w-1 shrink-0 rounded-full", BAR[item.type])} />
                    <span className="grid min-w-0 flex-1 gap-0.5">
                      <span className="truncate text-sm font-medium">{item.title}</span>
                      <span className="text-xs text-muted-foreground">{formatAnnouncementDate(item.updatedAt || item.createdAt)}</span>
                    </span>
                    {item.pinned ? <Pin className="mt-0.5 size-3.5 shrink-0 text-muted-foreground" aria-label={t("announcements.pinned")} /> : null}
                    {isUnread(item) ? <span role="img" className="mt-1 size-2 shrink-0 rounded-full bg-destructive" aria-label={t("announcements.unread")} /> : null}
                  </button>
                </li>
              ))}
            </ul>
          </div>

          <div className="min-h-48 overflow-y-auto p-5 md:max-h-[26rem]">
            {selected ? (
              <article className="grid gap-3">
                <header className="grid gap-1">
                  <h3 className="text-lg font-semibold leading-snug">{selected.title}</h3>
                  <p className="text-xs text-muted-foreground">{formatClock(selected.updatedAt || selected.createdAt)}</p>
                </header>
                <MiniMarkdown source={selected.contentMarkdown} />
              </article>
            ) : loading ? (
              <div role="status" aria-label={t("announcements.loading")} className="grid gap-3">
                <Skeleton className="h-6 w-1/2" />
                <Skeleton className="h-4 w-full" />
                <Skeleton className="h-4 w-4/5" />
                <span className="sr-only">{t("announcements.loading")}</span>
              </div>
            ) : (
              <p className="py-10 text-center text-sm text-muted-foreground">{t("announcements.empty")}</p>
            )}
          </div>
        </div>

        <DialogFooter className="border-t px-6 py-3">
          {mode === "auto" ? (
            <Button variant="outline" onClick={onDismissToday} disabled={saving}>
              {t("announcements.dismissToday")}
            </Button>
          ) : null}
          <Button onClick={onClose} loading={saving}>
            {t("common.actions.close")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
