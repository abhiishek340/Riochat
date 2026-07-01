"use client";

import { useRef, useState } from "react";
import { ArrowUpToLine, Pencil, Trash2 } from "lucide-react";
import { Tooltip } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import type { QueuedMessage } from "../../lib/queue";

export function QueuedList({ items, onEdit, onRemove, onGuide }: { items: QueuedMessage[]; onEdit: (id: string, text: string) => void; onRemove: (id: string) => void; onGuide: (id: string) => void }) {
  const t = useT();
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  // Escape cancels: the blur that follows must not save.
  const cancelled = useRef(false);
  if (items.length === 0) return null;
  const commit = (id: string) => {
    if (cancelled.current) {
      cancelled.current = false;
      return;
    }
    onEdit(id, draft);
    setEditing(null);
  };
  return (
    <ul className="mb-2 grid gap-1.5" aria-label={t("chatComposer.queue.title")}>
      {items.map((q) => (
        <li key={q.id} className="flex items-center gap-2 rounded-xl border bg-muted/40 px-3 py-1.5 text-sm">
          {editing === q.id ? (
            <input
              autoFocus
              value={draft}
              onChange={(e) => setDraft(e.target.value)}
              onBlur={() => commit(q.id)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  commit(q.id);
                } else if (e.key === "Escape") {
                  e.preventDefault();
                  cancelled.current = true;
                  setEditing(null);
                }
              }}
              placeholder={t("chatComposer.queue.editPlaceholder")}
              aria-label={t("chatComposer.queue.editPlaceholder")}
              className="h-7 min-w-0 flex-1 rounded-md border bg-background px-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring"
            />
          ) : (
            <span className="min-w-0 flex-1 truncate">
              {q.text || (q.attachments.length > 0 ? t("chatComposer.queue.attachmentOnly", { count: q.attachments.length }) : t("chatComposer.queue.emptyMessage"))}
              {q.text && q.attachments.length > 0 ? <span className="ml-2 text-xs text-muted-foreground">{t("chatComposer.queue.attachments", { count: q.attachments.length })}</span> : null}
            </span>
          )}
          <Tooltip label={t("chatComposer.queue.guide")}>
            <button type="button" aria-label={t("chatComposer.queue.guide")} onClick={() => onGuide(q.id)} className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground">
              <ArrowUpToLine className="size-3.5" />
            </button>
          </Tooltip>
          <Tooltip label={t("chatComposer.queue.edit")}>
            <button
              type="button"
              aria-label={t("chatComposer.queue.edit")}
              onClick={() => {
                cancelled.current = false;
                setDraft(q.text);
                setEditing(q.id);
              }}
              className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-foreground"
            >
              <Pencil className="size-3.5" />
            </button>
          </Tooltip>
          <Tooltip label={t("chatComposer.queue.delete")}>
            <button type="button" aria-label={t("chatComposer.queue.delete")} onClick={() => onRemove(q.id)} className="rounded p-1 text-muted-foreground hover:bg-accent hover:text-destructive">
              <Trash2 className="size-3.5" />
            </button>
          </Tooltip>
        </li>
      ))}
    </ul>
  );
}
