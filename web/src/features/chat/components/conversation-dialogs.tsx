"use client";

import { useEffect, useState } from "react";
import { Check, Copy, Link2, Plus, X } from "lucide-react";
import {
  Badge,
  Button,
  ConfirmDialog,
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
  Field,
  FieldError,
  FieldHint,
  FieldLabel,
  Input,
  NativeSelect,
  Skeleton,
  Switch,
  Textarea,
  toast,
} from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { createProject, createShare, deleteProject, getShare, revokeShare, updateProject, type ShareDTO } from "../api";
import { copyText } from "../lib/clipboard";
import { parseLabels, upsertProject, removeProject } from "../state/conversation-store";
import { useModelsState } from "../state/resources";
import type { ConversationDTO, ProjectDTO } from "../types";

export function RenameDialog({ conversation, open, onOpenChange, onSave }: { conversation: ConversationDTO | null; open: boolean; onOpenChange: (open: boolean) => void; onSave: (title: string) => Promise<boolean> }) {
  const t = useT();
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) setValue(conversation?.title ?? "");
  }, [open, conversation?.title]);
  const submit = async () => {
    if (!value.trim()) return;
    setBusy(true);
    const ok = await onSave(value);
    setBusy(false);
    if (ok) onOpenChange(false);
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("chatNav.rename.title")}</DialogTitle>
          <DialogDescription>{t("chatNav.rename.description")}</DialogDescription>
        </DialogHeader>
        <form
          onSubmit={(e) => {
            e.preventDefault();
            void submit();
          }}
          className="grid gap-4"
        >
          <Input value={value} maxLength={255} autoFocus onChange={(e) => setValue(e.target.value)} placeholder={t("chatNav.rename.placeholder")} aria-label={t("chatNav.rename.placeholder")} />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={busy} disabled={!value.trim()}>
              {t("common.actions.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

const MAX_LABELS = 6;
const MAX_LABEL_LENGTH = 24;

export function LabelsDialog({ conversation, open, onOpenChange, onSave }: { conversation: ConversationDTO | null; open: boolean; onOpenChange: (open: boolean) => void; onSave: (labels: string[]) => Promise<boolean> }) {
  const t = useT();
  const [labels, setLabels] = useState<string[]>([]);
  const [draft, setDraft] = useState("");
  const [error, setError] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) {
      setLabels(parseLabels(conversation?.labelsJSON));
      setDraft("");
      setError("");
    }
  }, [open, conversation?.labelsJSON]);

  const add = () => {
    const label = draft.trim();
    if (!label) return;
    if (Array.from(label).length > MAX_LABEL_LENGTH) return setError(t("chatNav.labels.tooLong"));
    if (labels.includes(label)) return setDraft("");
    if (labels.length >= MAX_LABELS) return setError(t("chatNav.labels.limit"));
    setLabels([...labels, label]);
    setDraft("");
    setError("");
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("chatNav.labels.title")}</DialogTitle>
          <DialogDescription>{t("chatNav.labels.description")}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap gap-1.5">
          {labels.map((label) => (
            <Badge key={label} tone="primary" className="gap-1 pr-1">
              {label}
              <button type="button" aria-label={t("chatNav.labels.remove", { label })} className="rounded-full p-0.5 hover:bg-primary/20" onClick={() => setLabels(labels.filter((l) => l !== label))}>
                <X className="size-3" />
              </button>
            </Badge>
          ))}
        </div>
        <div className="flex gap-2">
          <Input
            value={draft}
            onChange={(e) => setDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === "Enter" && !e.nativeEvent.isComposing) {
                e.preventDefault();
                add();
              }
            }}
            placeholder={t("chatNav.labels.placeholder")}
            aria-label={t("chatNav.labels.placeholder")}
          />
          <Button type="button" variant="outline" onClick={add}>
            <Plus /> {t("chatNav.labels.add")}
          </Button>
        </div>
        {error ? <p className="text-sm text-destructive">{error}</p> : null}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button
            loading={busy}
            onClick={async () => {
              setBusy(true);
              const ok = await onSave(labels);
              setBusy(false);
              if (ok) onOpenChange(false);
            }}
          >
            {t("common.actions.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function MoveDialog({ conversation, projects, open, onOpenChange, onMove }: { conversation: ConversationDTO | null; projects: ProjectDTO[]; open: boolean; onOpenChange: (open: boolean) => void; onMove: (projectId: string, name: string) => Promise<boolean> }) {
  const t = useT();
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) setValue(conversation?.projectID ?? "");
  }, [open, conversation?.projectID]);
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("chatNav.move.title")}</DialogTitle>
          <DialogDescription>{t("chatNav.move.description")}</DialogDescription>
        </DialogHeader>
        <NativeSelect value={value} onChange={(e) => setValue(e.target.value)} aria-label={t("chatNav.move.title")}>
          <option value="">{t("chatNav.move.unassigned")}</option>
          {projects.map((p) => (
            <option key={p.publicID} value={p.publicID}>
              {p.name}
            </option>
          ))}
        </NativeSelect>
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button
            loading={busy}
            onClick={async () => {
              setBusy(true);
              const ok = await onMove(value, projects.find((p) => p.publicID === value)?.name ?? "");
              setBusy(false);
              if (ok) onOpenChange(false);
            }}
          >
            {t("common.actions.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export function DeleteConversationDialog({ conversation, open, onOpenChange, defaultDeleteFiles, onDelete }: { conversation: ConversationDTO | null; open: boolean; onOpenChange: (open: boolean) => void; defaultDeleteFiles: boolean; onDelete: (deleteFiles: boolean) => Promise<void> }) {
  const t = useT();
  const [files, setFiles] = useState(defaultDeleteFiles);
  useEffect(() => {
    if (open) setFiles(defaultDeleteFiles);
  }, [open, defaultDeleteFiles]);
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("chatNav.delete.title")}
      description={
        <span className="grid gap-3">
          <span>{conversation?.title || t("chatNav.sidebar.untitled")}</span>
          <span>{t("chatNav.delete.description")}</span>
          <label className="flex items-center gap-2 text-foreground">
            <Switch checked={files} onCheckedChange={setFiles} aria-label={t("chatNav.delete.alsoFiles")} />
            {t("chatNav.delete.alsoFiles")}
          </label>
        </span>
      }
      confirmLabel={t("chatNav.delete.confirm")}
      destructive
      onConfirm={() => onDelete(files)}
    />
  );
}
