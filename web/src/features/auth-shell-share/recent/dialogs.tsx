"use client";

import { useEffect, useRef, useState, type FormEvent } from "react";
import { Check, Copy, Link2, RefreshCw, Sparkles, X } from "lucide-react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { toast } from "@/components/ui/toast-store";
import { resolveApiBase } from "@/lib/api/base-url";
import { publicWebOrigin } from "@/lib/api/platform";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { copyText } from "../lib/clipboard";
import { createShare, fetchShare, regenerateTitle, renameConversation, revokeShare, setLabels, type ShareInfo } from "./api";
import { Checkbox } from "./checkbox";
import type { ConversationRow } from "./model";

export function RenameDialog({
  row,
  open,
  onOpenChange,
  onRenamed,
}: {
  row: ConversationRow | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRenamed: (id: string, title: string) => void;
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [title, setTitle] = useState("");
  const [busy, setBusy] = useState(false);
  const [auto, setAuto] = useState(false);

  useEffect(() => {
    if (open) setTitle(row?.title.trim() || t("recentAll.untitled"));
  }, [open, row, t]);

  if (!row) return null;
  const submit = async (event: FormEvent) => {
    event.preventDefault();
    const next = title.trim();
    // A conversation without a title is shown (and prefilled) as the placeholder; saving that text as is changes nothing.
    const shown = row.title.trim() || t("recentAll.untitled");
    if (!next || next === shown) {
      onOpenChange(false);
      return;
    }
    setBusy(true);
    try {
      await renameConversation(row.publicID, next);
      onRenamed(row.publicID, next);
      onOpenChange(false);
    } catch (error) {
      toast.error(t("recentAll.rename.failed"), { description: errorMessage(error) });
    } finally {
      setBusy(false);
    }
  };

  const regenerate = async () => {
    setAuto(true);
    try {
      const next = await regenerateTitle(row.publicID);
      if (next) onRenamed(row.publicID, next);
      onOpenChange(false);
    } catch (error) {
      toast.error(t("recentAll.rename.autoFailed"), { description: errorMessage(error) });
    } finally {
      setAuto(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && !auto && onOpenChange(next)}>
      <DialogContent>
        <form onSubmit={submit} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{t("recentAll.rename.title")}</DialogTitle>
            <DialogDescription className="sr-only">{t("recentAll.rename.description")}</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel>{t("recentAll.rename.label")}</FieldLabel>
            <div className="flex gap-2">
              <Input autoFocus maxLength={255} value={title} onChange={(e) => setTitle(e.target.value)} onFocus={(e) => e.currentTarget.select()} />
              <Button type="button" variant="outline" size="icon" onClick={() => void regenerate()} loading={auto} aria-label={t("recentAll.rename.auto")} title={t("recentAll.rename.auto")}>
                <Sparkles />
              </Button>
            </div>
          </Field>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={busy}>
              {t("common.actions.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export const MAX_LABELS = 6;
export const MAX_LABEL_LENGTH = 24;

export function LabelsDialog({
  row,
  open,
  onOpenChange,
  onSaved,
}: {
  row: ConversationRow | null;
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onSaved: (id: string, labels: string[]) => void;
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [labels, setLabelList] = useState<string[]>([]);
  const [draft, setDraft] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (open) {
      setLabelList(row?.labels ?? []);
      setDraft("");
    }
  }, [open, row]);

  if (!row) return null;
  const add = () => {
    const value = draft.trim().slice(0, MAX_LABEL_LENGTH);
    if (!value) return;
    if (!labels.includes(value) && labels.length < MAX_LABELS) setLabelList([...labels, value]);
    setDraft("");
  };

  const save = async (event: FormEvent) => {
    event.preventDefault();
    const pending = draft.trim().slice(0, MAX_LABEL_LENGTH);
    const next = pending && !labels.includes(pending) && labels.length < MAX_LABELS ? [...labels, pending] : labels;
    setBusy(true);
    try {
      await setLabels(row.publicID, next);
      onSaved(row.publicID, next);
      onOpenChange(false);
    } catch (error) {
      toast.error(t("recentAll.labels.failed"), { description: errorMessage(error) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent>
        <form onSubmit={save} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{t("recentAll.labels.title")}</DialogTitle>
            <DialogDescription>{t("recentAll.labels.description", { max: MAX_LABELS })}</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel>{t("recentAll.labels.add")}</FieldLabel>
            <Input
              autoFocus
              maxLength={MAX_LABEL_LENGTH}
              value={draft}
              disabled={labels.length >= MAX_LABELS}
              onChange={(e) => setDraft(e.target.value)}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === ",") {
                  e.preventDefault();
                  add();
                }
              }}
            />
            <FieldHint>{t("recentAll.labels.hint", { max: MAX_LABEL_LENGTH })}</FieldHint>
          </Field>
          <ul className="flex flex-wrap gap-1.5" aria-label={t("recentAll.labels.current")}>
            {labels.map((label) => (
              <li key={label}>
                <Badge tone="primary" className="gap-1 pr-1">
                  {label}
                  <button
                    type="button"
                    onClick={() => setLabelList(labels.filter((l) => l !== label))}
                    aria-label={t("recentAll.labels.remove", { label })}
                    className="rounded-full p-0.5 hover:bg-primary/20"
                  >
                    <X className="size-3" />
                  </button>
                </Badge>
              </li>
            ))}
          </ul>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)} disabled={busy}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={busy}>
              {t("common.actions.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
