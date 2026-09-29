"use client";

import { BookOpenText, Pencil, Plus, Trash2 } from "lucide-react";
import { useEffect, useRef, useState, type FormEvent } from "react";
import { usePageTitle } from "@/components/shell/header-context";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { EmptyState } from "@/components/ui/empty-state";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import { LoadErrorNotice, SettingsPage, SettingsSection } from "../parts";
import { useLoad } from "../lib/use-load";
import {
  createSkill,
  discover,
  getSkill,
  listMine,
  normalizeTrigger,
  patchSkill,
  removeSkill,
  SKILL_LIMITS,
  validateSkill,
  type SkillFull,
  type SkillInput,
  type SkillSummary,
} from "./api";

const EMPTY: SkillInput = { title: "", trigger: "", description: "", markdown: "" };

export function SkillsSettingsPage() {
  const t = useT();
  const errorMessage = useErrorMessage();
  usePageTitle(t("settings.title"));
  const mine = useLoad((signal) => listMine(signal), "mine");
  const all = useLoad((signal) => discover(signal), "all");
  const [override, setOverride] = useState<SkillSummary[] | null>(null);
  const [editor, setEditor] = useState<{ skill: SkillSummary | null } | null>(null);
  const [removing, setRemoving] = useState<SkillSummary | null>(null);
  const [viewing, setViewing] = useState<SkillSummary | null>(null);
  const [busyId, setBusyId] = useState<number | null>(null);

  useEffect(() => {
    if (mine.data) setOverride(null);
  }, [mine.data]);
  useEffect(() => {
    const failure = mine.error ?? all.error;
    if (failure) toast.error(t("settings.skills.toasts.loadFailed"), { description: errorMessage(failure) });
    // Report each failure once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [mine.error, all.error]);

  const own = override ?? mine.data ?? [];
  const loadedRef = useRef<SkillSummary[]>([]);
  loadedRef.current = mine.data ?? [];
  // Requests finish in any order: each result edits the list as it is then.
  const edit = (change: (current: SkillSummary[]) => SkillSummary[]) => setOverride((prev) => change(prev ?? loadedRef.current));
  const listFailed = mine.error !== undefined && !mine.data && !override;
  const builtin = (all.data ?? []).filter((s) => s.scope !== "user");

  const toggle = async (s: SkillSummary, enabled: boolean) => {
    setBusyId(s.id);
    try {
      await patchSkill(s.id, { enabled });
      edit((current) => current.map((x) => (x.id === s.id ? { ...x, enabled } : x)));
    } catch (e) {
      toast.error(t("settings.skills.toasts.saveFailed"), { description: errorMessage(e) });
    } finally {
      setBusyId(null);
    }
  };

  return (
    <SettingsPage
      title={t("settings.nav.skills")}
      description={t("settings.skills.description")}
      actions={
        <Button size="sm" onClick={() => setEditor({ skill: null })}>
          <Plus />
          {t("settings.skills.add")}
        </Button>
      }
    >
      <SettingsSection title={t("settings.skills.mine")} bare>
        {mine.loading && !mine.data ? (
          <div className="grid gap-2 rounded-xl border bg-card p-4">
            <Skeleton className="h-5 w-1/3" />
            <Skeleton className="h-4 w-2/3" />
          </div>
        ) : listFailed ? (
          <LoadErrorNotice title={t("settings.skills.toasts.loadFailed")} error={mine.error} onRetry={mine.reload} />
        ) : own.length === 0 ? (
          <div className="rounded-xl border bg-card">
            <EmptyState
              icon={<BookOpenText />}
              title={t("settings.skills.emptyTitle")}
              description={t("settings.skills.emptyBody")}
              action={
                <Button size="sm" onClick={() => setEditor({ skill: null })}>
                  <Plus />
                  {t("settings.skills.add")}
                </Button>
              }
            />
          </div>
        ) : (
          <ul className="divide-y rounded-xl border bg-card shadow-sm">
            {own.map((s) => (
              <li key={s.id} className="flex flex-wrap items-center gap-3 px-4 py-3">
                <div className="grid min-w-0 flex-1 gap-0.5">
                  <span className="flex items-center gap-2">
                    <span className="truncate text-sm font-medium">{s.title}</span>
                    <Badge tone="outline" className="font-mono">
                      /{s.trigger}
                    </Badge>
                  </span>
                  {s.description ? <span className="truncate text-[13px] text-muted-foreground">{s.description}</span> : null}
                </div>
                <div className="flex items-center gap-1.5">
                  <Switch checked={s.enabled} disabled={busyId === s.id} aria-label={t("settings.skills.enabledNamed", { title: s.title })} onCheckedChange={(v) => void toggle(s, v)} />
                  <Button variant="ghost" size="icon-sm" aria-label={t("settings.skills.editNamed", { title: s.title })} onClick={() => setEditor({ skill: s })}>
                    <Pencil />
                  </Button>
                  <Button variant="ghost" size="icon-sm" aria-label={t("settings.skills.deleteNamed", { title: s.title })} onClick={() => setRemoving(s)}>
                    <Trash2 />
                  </Button>
                </div>
              </li>
            ))}
          </ul>
        )}
      </SettingsSection>

      {builtin.length > 0 ? (
        <SettingsSection title={t("settings.skills.builtin")} description={t("settings.skills.builtinHint")}>
          {builtin.map((s) => (
            <button
              key={s.id}
              type="button"
              onClick={() => setViewing(s)}
              className="flex w-full flex-wrap items-center gap-3 px-4 py-3 text-left outline-none transition-colors hover:bg-muted/40 focus-visible:ring-2 focus-visible:ring-inset focus-visible:ring-ring"
            >
              <span className="grid min-w-0 flex-1 gap-0.5">
                <span className="flex items-center gap-2">
                  <span className="truncate text-sm font-medium">{s.title}</span>
                  <Badge tone="outline" className="font-mono">
                    /{s.trigger}
                  </Badge>
                </span>
                {s.description ? <span className="truncate text-[13px] text-muted-foreground">{s.description}</span> : null}
              </span>
            </button>
          ))}
        </SettingsSection>
      ) : null}

      <SkillEditor
        target={editor}
        onOpenChange={(open) => !open && setEditor(null)}
        onSaved={(saved) => {
          edit((current) => (editor?.skill ? current.map((x) => (x.id === saved.id ? saved : x)) : [saved, ...current]));
          all.reload();
        }}
      />
      <SkillViewer skill={viewing} onOpenChange={(open) => !open && setViewing(null)} />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        destructive
        title={t("settings.skills.deleteTitle")}
        description={t("settings.skills.deleteBody", { title: removing?.title ?? "" })}
        confirmLabel={t("common.actions.delete")}
        onConfirm={async () => {
          if (!removing) return;
          await removeSkill(removing.id);
          edit((current) => current.filter((x) => x.id !== removing.id));
          all.reload();
          toast.success(t("settings.skills.toasts.deleted"));
        }}
      />
    </SettingsPage>
  );
}

function SkillEditor({
  target,
  onOpenChange,
  onSaved,
}: {
  target: { skill: SkillSummary | null } | null;
  onOpenChange: (open: boolean) => void;
  onSaved: (skill: SkillSummary) => void;
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const open = target !== null;
  const editing = target?.skill ?? null;
  const [draft, setDraft] = useState<SkillInput>(EMPTY);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);

  useEffect(() => {
    if (!target) return;
    if (!target.skill) {
      setDraft(EMPTY);
      return;
    }
    const id = target.skill.id;
    setDraft({ title: target.skill.title, trigger: target.skill.trigger, description: target.skill.description, markdown: "" });
    setLoading(true);
    const controller = new AbortController();
    getSkill(id, controller.signal)
      .then((full: SkillFull | null) => {
        if (!controller.signal.aborted && full) setDraft((d) => ({ ...d, markdown: full.markdown }));
      })
      .catch((e: unknown) => {
        if (!controller.signal.aborted) toast.error(t("settings.skills.toasts.loadFailed"), { description: errorMessage(e) });
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
    // Seed when the target changes.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [target]);

  const valid = validateSkill(draft);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!valid || saving) return;
    setSaving(true);
    try {
      const body = { ...draft, title: draft.title.trim(), trigger: normalizeTrigger(draft.trigger), description: draft.description.trim() };
      const saved = editing ? await patchSkill(editing.id, body) : await createSkill(body);
      if (saved) {
        onSaved(saved);
        toast.success(editing ? t("settings.skills.toasts.updated") : t("settings.skills.toasts.created"));
        onOpenChange(false);
      }
    } catch (err) {
      toast.error(t("settings.skills.toasts.saveFailed"), { description: errorMessage(err) });
    } finally {
      setSaving(false);
    }
  };

  const set = (patch: Partial<SkillInput>) => setDraft((d) => ({ ...d, ...patch }));

  return (
    <Dialog open={open} onOpenChange={(next) => (saving ? undefined : onOpenChange(next))}>
      <DialogContent className="max-w-xl">
        <form onSubmit={(e) => void submit(e)} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{editing ? t("settings.skills.editTitle") : t("settings.skills.addTitle")}</DialogTitle>
            <DialogDescription>{t("settings.skills.editorBody")}</DialogDescription>
          </DialogHeader>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field required>
              <FieldLabel>{t("settings.skills.fieldTitle")}</FieldLabel>
              <Input value={draft.title} maxLength={SKILL_LIMITS.title} onChange={(e) => set({ title: e.target.value })} />
            </Field>
            <Field required>
              <FieldLabel>{t("settings.skills.fieldTrigger")}</FieldLabel>
              <div className="flex items-center gap-1.5">
                <span className="font-mono text-muted-foreground">/</span>
                <Input value={draft.trigger} maxLength={SKILL_LIMITS.trigger + 4} className="font-mono" onChange={(e) => set({ trigger: e.target.value })} />
              </div>
              <FieldHint>{t("settings.skills.triggerHint")}</FieldHint>
            </Field>
          </div>
          <Field>
            <FieldLabel>{t("settings.skills.fieldDescription")}</FieldLabel>
            <Input value={draft.description} maxLength={SKILL_LIMITS.description} onChange={(e) => set({ description: e.target.value })} />
          </Field>
          <Field required>
            <FieldLabel>{t("settings.skills.fieldMarkdown")}</FieldLabel>
            <Textarea
              value={draft.markdown}
              maxLength={SKILL_LIMITS.markdown}
              disabled={loading}
              className="h-56 resize-y font-mono text-[13px]"
              onChange={(e) => set({ markdown: e.target.value })}
            />
            <FieldHint>{t("settings.skills.markdownHint", { count: draft.markdown.length, max: SKILL_LIMITS.markdown })}</FieldHint>
          </Field>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={saving} onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={saving} disabled={!valid || loading}>
              {t("common.actions.save")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function SkillViewer({ skill, onOpenChange }: { skill: SkillSummary | null; onOpenChange: (open: boolean) => void }) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [body, setBody] = useState("");
  const [loading, setLoading] = useState(false);

  useEffect(() => {
    if (!skill) return;
    setBody("");
    setLoading(true);
    const controller = new AbortController();
    getSkill(skill.id, controller.signal)
      .then((full) => {
        if (!controller.signal.aborted) setBody(full?.markdown ?? "");
      })
      .catch((e: unknown) => {
        if (!controller.signal.aborted) toast.error(t("settings.skills.toasts.loadFailed"), { description: errorMessage(e) });
      })
      .finally(() => {
        if (!controller.signal.aborted) setLoading(false);
      });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [skill?.id]);

  return (
    <Dialog open={skill !== null} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{skill?.title}</DialogTitle>
          <DialogDescription>{skill ? `/${skill.trigger}${skill.description ? ` · ${skill.description}` : ""}` : ""}</DialogDescription>
        </DialogHeader>
        {loading ? <Skeleton className="h-40 w-full" /> : <pre className="max-h-[50vh] overflow-auto whitespace-pre-wrap rounded-lg bg-muted p-3 font-mono text-[13px]">{body}</pre>}
        <DialogFooter>
          <Button onClick={() => onOpenChange(false)}>{t("common.actions.close")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
