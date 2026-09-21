"use client";

import { Trash2 } from "lucide-react";
import { Badge, Button, Switch, Tooltip } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { displayName, summaryOf, type LibraryItem } from "./model";

/** One skill or prompt. The card opens it; the switch and delete button do not. */
export function ItemCard({
  item,
  onOpen,
  onToggle,
  onDelete,
}: {
  item: LibraryItem;
  onOpen: () => void;
  onToggle: (enabled: boolean) => void;
  onDelete: () => void;
}) {
  const t = useT();
  const own = item.scope === "user";
  const name = displayName(item);
  return (
    <li
      data-disabled={!item.enabled || undefined}
      // Clicking the card's padding opens it too; the real control for keyboards and
      // assistive technology is the button below, so the list keeps its semantics.
      onClick={(e) => e.target === e.currentTarget && onOpen()}
      className={cn(
        "group flex cursor-pointer items-center gap-3 rounded-xl border bg-card px-4 py-3 transition-colors hover:bg-accent/40 focus-within:bg-accent/40",
        !item.enabled && "opacity-60",
      )}
    >
      <button
        type="button"
        onClick={onOpen}
        className="min-w-0 flex-1 rounded-md text-left outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <span className="flex items-center gap-2">
          <span className="truncate font-mono text-sm font-semibold">/{name}</span>
          {!own ? <Badge tone="primary">{t("libraryWs.badges.builtin")}</Badge> : null}
        </span>
        <span className="mt-0.5 block truncate text-sm text-muted-foreground">{summaryOf(item)}</span>
      </button>
      {own ? (
        <div className="flex shrink-0 items-center gap-2">
          <Tooltip label={t("libraryWs.card.deleteLabel", { name })}>
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label={t("libraryWs.card.deleteLabel", { name })}
              className="hover:text-destructive"
              onClick={onDelete}
            >
              <Trash2 />
            </Button>
          </Tooltip>
          <Switch checked={item.enabled} aria-label={t("libraryWs.card.toggle", { name })} onCheckedChange={onToggle} />
        </div>
      ) : null}
    </li>
  );
}
