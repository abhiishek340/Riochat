"use client";

import { Check, Pencil, Plus, Trash2, X } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast-store";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { deleteMemory, localEntry, MEMORY_KEY_MAX, MEMORY_VALUE_MAX, saveMemory, upsertEntry, type MemoryEntry, type MemoryScope } from "./api";

/**
 * Editable list of remembered notes for one scope: add in a dialog, edit the
 * value in place, delete after a second click. The parent owns the list.
 */
export function MemoryList({
  entries,
  loading,
  scope,
  limit,
  onChange,
}: {
  entries: MemoryEntry[];
  loading: boolean;
  scope: MemoryScope;
  /** Maximum number of notes, when the list is capped. */
  limit?: number;
  onChange: (next: MemoryEntry[]) => void;
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [addOpen, setAddOpen] = useState(false);
  const [name, setName] = useState("");
  const [content, setContent] = useState("");
  const [adding, setAdding] = useState(false);
  const [editing, setEditing] = useState<string | null>(null);
  const [draft, setDraft] = useState("");
  const [savingKey, setSavingKey] = useState<string | null>(null);
  const [confirming, setConfirming] = useState<string | null>(null);
  const [deletingKey, setDeletingKey] = useState<string | null>(null);

  const full = limit !== undefined && entries.length >= limit;
  // Saves and deletes finish at different times: results are applied to the
  // list as it is when they land, not as it was when they started.
  const latest = useRef(entries);
  latest.current = entries;

  const closeAdd = (open: boolean) => {
    if (adding) return;
    setAddOpen(open);
    if (!open) {
      setName("");
      setContent("");
    }
  };

  const add = async (e: FormEvent) => {
    e.preventDefault();
    const key = name.trim();
    const value = content.trim();
    if (!key || !value || adding) return;
    const exists = entries.some((x) => x.memoryKey === key);
    if (full && !exists) return;
    setAdding(true);
    try {
      await saveMemory({ memoryKey: key, value, scope });
      onChange(upsertEntry(latest.current, localEntry(key, value, scope)));
      setAddOpen(false);
      setName("");
      setContent("");
      toast.success(t("settings.memory.toasts.added"));
    } catch (error) {
      toast.error(t("settings.memory.toasts.addFailed"), { description: errorMessage(error) });
    } finally {
      setAdding(false);
    }
  };

  const commitEdit = async (entry: MemoryEntry) => {
    const value = draft.trim();
    if (!value || value === entry.value) {
      setEditing(null);
      return;
    }
    setSavingKey(entry.memoryKey);
    try {
      await saveMemory({ memoryKey: entry.memoryKey, value, scope });
      onChange(upsertEntry(latest.current, localEntry(entry.memoryKey, value, scope)));
      setEditing(null);
      toast.success(t("settings.memory.toasts.updated"));
    } catch (error) {
      toast.error(t("settings.memory.toasts.updateFailed"), { description: errorMessage(error) });
    } finally {
      setSavingKey(null);
    }
  };

  const remove = async (entry: MemoryEntry) => {
    setDeletingKey(entry.memoryKey);
    try {
      await deleteMemory(entry.memoryKey);
      onChange(latest.current.filter((x) => x.memoryKey !== entry.memoryKey));
      setConfirming(null);
      toast.success(t("settings.memory.toasts.deleted"));
    } catch (error) {
      toast.error(t("settings.memory.toasts.deleteFailed"), { description: errorMessage(error) });
    } finally {
      setDeletingKey(null);
    }
  };

  return (
    <div className="grid gap-3">
      <div className="flex items-center justify-between gap-3">
        <span className="text-sm text-muted-foreground tabular-nums">
          {limit !== undefined
            ? t("settings.memory.counter", { count: loading ? "--" : entries.length, limit })
            : loading
              ? "--"
              : t("settings.memory.count", { count: entries.length })}
        </span>
        <Button size="sm" disabled={loading || full} onClick={() => setAddOpen(true)}>
          {full ? null : <Plus />}
          {full ? t("settings.memory.full") : t("settings.memory.add")}
        </Button>
      </div>

      <ul className="divide-y rounded-lg border bg-card">
        {loading ? (
          Array.from({ length: 3 }, (_, i) => (
            <li key={i} className="px-4 py-3">
              <Skeleton className="h-4 w-2/3" />
            </li>
          ))
        ) : entries.length === 0 ? (
          <li className="px-4 py-8 text-center text-sm text-muted-foreground">{t("settings.memory.empty")}</li>
        ) : (
          entries.map((entry) => (
            <MemoryRow
              key={entry.memoryKey}
              entry={entry}
              editing={editing === entry.memoryKey}
              draft={draft}
              saving={savingKey === entry.memoryKey}
              confirming={confirming === entry.memoryKey}
              deleting={deletingKey === entry.memoryKey}
              onDraft={setDraft}
              onEdit={() => {
                setEditing(entry.memoryKey);
                setDraft(entry.value);
                setConfirming(null);
              }}
              onCancelEdit={() => setEditing(null)}
              onCommit={() => void commitEdit(entry)}
              onAskDelete={() => {
                setConfirming(entry.memoryKey);
                setEditing(null);
              }}
              onCancelDelete={() => setConfirming(null)}
              onDelete={() => void remove(entry)}
            />
          ))
        )}
      </ul>

      <Dialog open={addOpen} onOpenChange={closeAdd}>
        <DialogContent>
          <form onSubmit={(e) => void add(e)} className="grid gap-4">
            <DialogHeader>
              <DialogTitle>{t("settings.memory.addTitle")}</DialogTitle>
              <DialogDescription>{t("settings.memory.addDescription")}</DialogDescription>
            </DialogHeader>
            <Field>
              <FieldLabel>{t("settings.memory.name")}</FieldLabel>
              <Input value={name} maxLength={MEMORY_KEY_MAX} disabled={adding || full} autoComplete="off" onChange={(e) => setName(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>{t("settings.memory.content")}</FieldLabel>
              <Textarea value={content} maxLength={MEMORY_VALUE_MAX} disabled={adding || full} className="h-28 resize-none" onChange={(e) => setContent(e.target.value)} />
              <FieldHint>{t("settings.memory.contentHint")}</FieldHint>
            </Field>
            <DialogFooter>
              <Button type="button" variant="outline" disabled={adding} onClick={() => closeAdd(false)}>
                {t("common.actions.cancel")}
              </Button>
              <Button type="submit" loading={adding} disabled={!name.trim() || !content.trim()}>
                {t("settings.memory.save")}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}

function MemoryRow({
  entry,
  editing,
  draft,
  saving,
  confirming,
  deleting,
  onDraft,
  onEdit,
  onCancelEdit,
  onCommit,
  onAskDelete,
  onCancelDelete,
  onDelete,
}: {
  entry: MemoryEntry;
  editing: boolean;
  draft: string;
  saving: boolean;
  confirming: boolean;
  deleting: boolean;
  onDraft: (v: string) => void;
  onEdit: () => void;
  onCancelEdit: () => void;
  onCommit: () => void;
  onAskDelete: () => void;
  onCancelDelete: () => void;
  onDelete: () => void;
}) {
  const t = useT();
  const area = useRef<HTMLTextAreaElement>(null);
  useEffect(() => {
    if (editing) area.current?.focus();
  }, [editing]);

  return (
    <li className="group grid gap-2 px-4 py-3">
      <div className="flex items-start justify-between gap-3">
        <div className="grid min-w-0 flex-1 gap-1">
          <span className="truncate text-sm font-medium">{entry.memoryKey}</span>
          {editing ? (
            <Textarea
              ref={area}
              value={draft}
              maxLength={MEMORY_VALUE_MAX}
              disabled={saving}
              className="min-h-16 resize-y"
              aria-label={t("settings.memory.content")}
              onChange={(e) => onDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" && !e.shiftKey && !e.nativeEvent.isComposing) {
                  e.preventDefault();
                  onCommit();
                } else if (e.key === "Escape") {
                  e.preventDefault();
                  onCancelEdit();
                }
              }}
            />
          ) : (
            <p className="whitespace-pre-wrap break-words text-sm text-muted-foreground">{entry.value}</p>
          )}
        </div>
        {!confirming ? (
          <div className={cn("flex shrink-0 gap-1 transition-opacity md:opacity-0 md:group-focus-within:opacity-100 md:group-hover:opacity-100", editing && "md:opacity-100")}>
            {editing ? (
              <>
                <Button variant="ghost" size="icon-sm" aria-label={t("common.actions.save")} loading={saving} onClick={onCommit}>
                  {saving ? null : <Check />}
                </Button>
                <Button variant="ghost" size="icon-sm" aria-label={t("common.actions.cancel")} disabled={saving} onClick={onCancelEdit}>
                  <X />
                </Button>
              </>
            ) : (
              <>
                <Button variant="ghost" size="icon-sm" aria-label={t("settings.memory.editNamed", { name: entry.memoryKey })} onClick={onEdit}>
                  <Pencil />
                </Button>
                <Button variant="ghost" size="icon-sm" aria-label={t("settings.memory.deleteNamed", { name: entry.memoryKey })} onClick={onAskDelete}>
                  <Trash2 />
                </Button>
              </>
            )}
          </div>
        ) : null}
      </div>
      {confirming ? (
        <div className="flex flex-wrap items-center justify-between gap-2 rounded-md bg-destructive/8 px-3 py-2 text-sm">
          <span>{t("settings.memory.confirmDelete")}</span>
          <span className="flex gap-2">
            <Button variant="outline" size="sm" disabled={deleting} onClick={onCancelDelete}>
              {t("common.actions.cancel")}
            </Button>
            <Button variant="destructive" size="sm" loading={deleting} onClick={onDelete}>
              {t("common.actions.delete")}
            </Button>
          </span>
        </div>
      ) : null}
    </li>
  );
}
