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

export function ShareDialog({ conversation, open, onOpenChange, defaultMessageIds }: { conversation: ConversationDTO | null; open: boolean; onOpenChange: (open: boolean) => void; defaultMessageIds: string[] }) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [share, setShare] = useState<ShareDTO | null>(null);
  const [loading, setLoading] = useState(false);
  const [busy, setBusy] = useState(false);
  const [copied, setCopied] = useState(false);
  const id = conversation?.publicID;

  useEffect(() => {
    if (!open || !id) return;
    let live = true;
    setLoading(true);
    getShare(id).then(
      (s) => live && setShare(s.status === "active" ? s : null),
      (e) => {
        if (live) toast.error(t("chatNav.share.loadFailed"), { description: errorMessage(e) });
      },
    ).finally(() => live && setLoading(false));
    return () => {
      live = false;
    };
  }, [open, id, t, errorMessage]);

  const link = share ? `${window.location.origin}/share?id=${encodeURIComponent(share.shareID)}` : "";
  const run = async (fn: () => Promise<ShareDTO | null>) => {
    setBusy(true);
    try {
      setShare(await fn());
    } catch (e) {
      toast.error(t("chatNav.share.failed"), { description: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("chatNav.share.title")}</DialogTitle>
          <DialogDescription>{t("chatNav.share.description")}</DialogDescription>
        </DialogHeader>
        {loading ? (
          <Skeleton className="h-10 w-full" />
        ) : share ? (
          <div className="grid gap-3">
            <div className="flex items-center gap-2">
              <Badge tone="success">{t("chatNav.share.active")}</Badge>
              <span className="text-xs text-muted-foreground">{t("chatNav.share.messages", { count: share.messageCount })}</span>
            </div>
            <div className="flex gap-2">
              <Input readOnly value={link} onFocus={(e) => e.currentTarget.select()} aria-label={t("chatNav.share.copy")} />
              <Button
                variant="outline"
                onClick={async () => {
                  if (await copyText(link)) {
                    setCopied(true);
                    toast.success(t("chatNav.share.copied"));
                    setTimeout(() => setCopied(false), 1500);
                  }
                }}
              >
                {copied ? <Check /> : <Copy />} {t("chatNav.share.copy")}
              </Button>
            </div>
            <DialogFooter className="sm:justify-between">
              <Button variant="outline" loading={busy} onClick={() => conversation && run(() => createShare(conversation.publicID, defaultMessageIds, true))}>
                <Link2 /> {t("chatNav.share.regenerate")}
              </Button>
              <Button variant="destructive" loading={busy} onClick={() => conversation && run(async () => (await revokeShare(conversation.publicID), null))}>
                {t("chatNav.share.revoke")}
              </Button>
            </DialogFooter>
          </div>
        ) : (
          <DialogFooter>
            <Badge tone="outline" className="mr-auto">
              {t("chatNav.share.inactive")}
            </Badge>
            <Button loading={busy} onClick={() => conversation && run(() => createShare(conversation.publicID, defaultMessageIds))}>
              {t("chatNav.share.create")}
            </Button>
          </DialogFooter>
        )}
      </DialogContent>
    </Dialog>
  );
}

export function ProjectDialog({ project, open, onOpenChange }: { project: ProjectDTO | null; open: boolean; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const models = useModelsState().data;
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [prompt, setPrompt] = useState("");
  const [model, setModel] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState("");
  useEffect(() => {
    if (!open) return;
    setName(project?.name ?? "");
    setDescription(project?.description ?? "");
    setPrompt(project?.systemPrompt ?? "");
    setModel(project?.defaultModel ?? "");
    setError("");
  }, [open, project]);

  const save = async () => {
    if (!name.trim()) return setError(t("chatNav.project.nameRequired"));
    setBusy(true);
    try {
      const body = { name: name.trim(), description: description.trim(), systemPrompt: prompt, defaultModel: model };
      upsertProject(project ? await updateProject(project.publicID, body) : await createProject(body));
      onOpenChange(false);
    } catch (e) {
      setError(t("chatNav.project.failed") + " " + errorMessage(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{project ? t("chatNav.project.editTitle") : t("chatNav.project.createTitle")}</DialogTitle>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(e) => {
            e.preventDefault();
            void save();
          }}
        >
          <Field>
            <FieldLabel>{t("chatNav.project.name")}</FieldLabel>
            <Input value={name} maxLength={80} autoFocus onChange={(e) => setName(e.target.value)} placeholder={t("chatNav.project.namePlaceholder")} />
          </Field>
          <Field>
            <FieldLabel>{t("chatNav.project.description")}</FieldLabel>
            <Input value={description} maxLength={255} onChange={(e) => setDescription(e.target.value)} placeholder={t("chatNav.project.descriptionPlaceholder")} />
          </Field>
          <Field>
            <FieldLabel>{t("chatNav.project.instructions")}</FieldLabel>
            <Textarea value={prompt} maxLength={12000} rows={5} onChange={(e) => setPrompt(e.target.value)} placeholder={t("chatNav.project.instructionsPlaceholder")} />
          </Field>
          <Field>
            <FieldLabel>{t("chatNav.project.defaultModel")}</FieldLabel>
            <NativeSelect value={model} onChange={(e) => setModel(e.target.value)}>
              <option value="">{t("chatNav.project.noDefaultModel")}</option>
              {models.map((m) => (
                <option key={m.platformModelName} value={m.platformModelName}>
                  {m.platformModelName}
                </option>
              ))}
            </NativeSelect>
            <FieldHint>{t("chatNav.project.modeHint")}</FieldHint>
          </Field>
          <FieldError>{error}</FieldError>
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={busy}>
              {project ? t("chatNav.project.save") : t("chatNav.project.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

export function DeleteProjectDialog({ project, open, onOpenChange, onDeleted }: { project: ProjectDTO | null; open: boolean; onOpenChange: (open: boolean) => void; onDeleted?: (id: string) => void }) {
  const t = useT();
  const [chats, setChats] = useState(false);
  const [files, setFiles] = useState(false);
  useEffect(() => {
    if (open) {
      setChats(false);
      setFiles(false);
    }
  }, [open]);
  return (
    <ConfirmDialog
      open={open}
      onOpenChange={onOpenChange}
      title={t("chatNav.project.deleteTitle")}
      description={
        <span className="grid gap-3">
          <span>{t("chatNav.project.deleteDescription")}</span>
          <label className="flex items-center gap-2 text-foreground">
            <Switch checked={chats} onCheckedChange={setChats} aria-label={t("chatNav.project.deleteChats")} />
            {t("chatNav.project.deleteChats")}
          </label>
          {chats ? (
            <label className="flex items-center gap-2 text-foreground">
              <Switch checked={files} onCheckedChange={setFiles} aria-label={t("chatNav.project.deleteFiles")} />
              {t("chatNav.project.deleteFiles")}
            </label>
          ) : null}
        </span>
      }
      confirmLabel={t("chatNav.project.delete")}
      destructive
      onConfirm={async () => {
        if (!project) return;
        await deleteProject(project.publicID, { deleteConversations: chats, deleteFiles: files });
        removeProject(project.publicID);
        onDeleted?.(project.publicID);
      }}
    />
  );
}
