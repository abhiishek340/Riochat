"use client";

import { Pencil, Plus, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast-store";
import { Tooltip } from "@/components/ui/tooltip";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { Section } from "../components/page";
import { Pagination } from "../components/pagination";
import { SettingsGroup } from "../components/settings-group";
import { SearchBox, Toolbar } from "../components/toolbar";
import { problemText } from "../lib/problem";
import type { FieldDef } from "../lib/settings-form";
import { useNotify } from "../lib/use-notify";
import { usePagedQuery } from "../lib/use-paged-query";
import { useRowUpdates } from "../lib/use-row-updates";
import type { SettingsForm } from "../lib/use-settings-form";
import { useSnapshot } from "../lib/use-snapshot";
import { createLibrary, deleteLibrary, listLibrary, updateLibrary, type ComponentItem, type LibraryItem, type LibraryKind, type PromptItem, type SkillItem } from "./api";
import { ComponentPreview } from "./component-preview";
import { LIMITS, normalizeTemplateName, parseSampleProps, validateComponent, validateTemplate } from "./rules";

const SKILLS_PROMPT: FieldDef = { ns: "chat", key: "skills_prompt", type: "textarea", label: "adminConversation.library.skillsPrompt", hint: "adminConversation.library.skillsPromptHint", def: "" };
export const LIBRARY_FIELDS: FieldDef[] = [SKILLS_PROMPT];

export function PromptLibrary({ form }: { form: SettingsForm }) {
  const t = useT();
  const [tab, setTab] = useState<LibraryKind>("skills");
  return (
    <Section title={t("adminConversation.library.title")} description={t("adminConversation.library.description")}>
      <Tabs value={tab} onValueChange={(v) => setTab(v as LibraryKind)}>
        <TabsList>
          <TabsTrigger value="skills">{t("adminConversation.library.tabs.skills")}</TabsTrigger>
          <TabsTrigger value="prompts">{t("adminConversation.library.tabs.prompts")}</TabsTrigger>
          <TabsTrigger value="components">{t("adminConversation.library.tabs.components")}</TabsTrigger>
        </TabsList>
        <TabsContent value="skills" className="space-y-4">
          <SettingsGroup form={form} fields={LIBRARY_FIELDS} className="shadow-none" />
          <LibraryList kind="skills" />
        </TabsContent>
        <TabsContent value="prompts">
          <LibraryList kind="prompts" />
        </TabsContent>
        <TabsContent value="components">
          <LibraryList kind="components" />
        </TabsContent>
      </Tabs>
    </Section>
  );
}

function itemName(kind: LibraryKind, item: LibraryItem): string {
  return kind === "components" ? (item as ComponentItem).name : (item as PromptItem | SkillItem).trigger || (item as PromptItem | SkillItem).title;
}

function LibraryList({ kind }: { kind: LibraryKind }) {
  const t = useT();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [editing, setEditing] = useState<LibraryItem | "new" | null>(null);
  const [removing, setRemoving] = useState<LibraryItem | null>(null);
  const filters = useMemo(() => ({ q }), [q]);
  const list = usePagedQuery<LibraryItem, { q: string }>({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) => listLibrary(kind, { page, pageSize, q: f.q }, signal),
    onError: (error) => notify.error("adminConversation.library.loadFailed", error),
  });
  const getKey = useCallback((x: LibraryItem) => x.id, []);
  const rows = useRowUpdates<LibraryItem, number>({ getKey, setRows: list.setRows });

  const columns: Column<LibraryItem>[] = [
    {
      id: "name",
      header: t("adminConversation.library.columns.name"),
      sticky: "left",
      cell: (x) => (
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="truncate font-medium">{kind === "components" ? itemName(kind, x) : `/${itemName(kind, x)}`}</span>
            {x.scope === "builtin" ? <Badge tone="outline">{t("adminConversation.library.builtin")}</Badge> : null}
          </div>
          {kind !== "components" && (x as PromptItem).title && (x as PromptItem).title !== (x as PromptItem).trigger ? <div className="truncate text-xs text-muted-foreground">{(x as PromptItem).title}</div> : null}
        </div>
      ),
    },
    { id: "description", header: t("adminConversation.library.columns.description"), className: "max-w-[28rem]", cell: (x) => <p className="line-clamp-2 text-[13px] text-muted-foreground">{x.description || "-"}</p> },
    {
      id: "enabled",
      header: t("adminConversation.library.columns.enabled"),
      cell: (x) => (
        <Switch
          checked={x.enabled}
          disabled={rows.isPending(x.id)}
          aria-label={`${t("adminConversation.library.columns.enabled")}: ${itemName(kind, x)}`}
          onCheckedChange={(on) => void rows.update(x, (r) => ({ ...r, enabled: on }), () => updateLibrary(kind, x.id, { enabled: on }), { failKey: "adminConversation.library.updateFailed", onSuccess: () => list.reload() })}
        />
      ),
    },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (x) => (
        <CellActions>
          <Tooltip label={t("common.actions.edit")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.edit")}: ${itemName(kind, x)}`} onClick={() => setEditing(x)}>
              <Pencil />
            </Button>
          </Tooltip>
          {x.scope !== "builtin" ? (
            <Tooltip label={t("common.actions.delete")}>
              <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.delete")}: ${itemName(kind, x)}`} onClick={() => setRemoving(x)}>
                <Trash2 className="text-destructive" />
              </Button>
            </Tooltip>
          ) : null}
        </CellActions>
      ),
    },
  ];

  return (
    <div className="space-y-3">
      <Toolbar className="justify-between">
        <SearchBox value={q} onChange={setQ} placeholder={t("adminConversation.library.search")} />
        <Button size="sm" onClick={() => setEditing("new")}>
          <Plus />
          {t(`adminConversation.library.new.${kind}`)}
        </Button>
      </Toolbar>
      <DataTable columns={columns} rows={list.rows} rowKey={getKey} loading={list.loading} error={list.error} onRetry={list.reload} fetching={list.fetching} empty={t("adminConversation.library.empty")} label={t(`adminConversation.library.tabs.${kind}`)} />
      <Pagination page={list.page} pageSize={list.pageSize} total={list.total} onPage={list.setPage} onPageSize={list.setPageSize} disabled={list.fetching} />

      {kind === "components" ? (
        <ComponentDialog target={editing as ComponentItem | "new" | null} onOpenChange={(open) => !open && setEditing(null)} onSaved={() => list.reload()} />
      ) : (
        <TemplateDialog kind={kind} target={editing as PromptItem | SkillItem | "new" | null} onOpenChange={(open) => !open && setEditing(null)} onSaved={() => list.reload()} />
      )}
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={t("adminConversation.library.deleteTitle", { name: removing ? itemName(kind, removing) : "" })}
        description={t("adminConversation.library.deleteBody")}
        confirmLabel={t("common.actions.delete")}
        destructive
        onConfirm={async () => {
          if (!removing) return;
          try {
            await deleteLibrary(kind, removing.id);
          } catch (error) {
            notify.error("adminConversation.library.deleteFailed", error);
            throw error;
          }
          notify.success("adminConversation.library.deleted");
          const left = list.rows.length - 1;
          if (left <= 0 && list.page > 1) list.setPage(list.page - 1);
          else list.reload();
        }}
      />
    </div>
  );
}

function TemplateDialog({ kind, target, onOpenChange, onSaved }: { kind: "prompts" | "skills"; target: PromptItem | SkillItem | "new" | null; onOpenChange: (open: boolean) => void; onSaved: () => void }) {
  const t = useT();
  const message = useErrorMessage();
  const shown = useSnapshot(target);
  const existing = shown && shown !== "new" ? shown : null;
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [content, setContent] = useState("");
  const [enabled, setEnabled] = useState(true);
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (target === null) return;
    const item = target === "new" ? null : target;
    setName(item ? item.trigger || item.title : "");
    setDescription(item?.description ?? "");
    setContent(item ? (kind === "skills" ? (item as SkillItem).markdown : (item as PromptItem).content) : "");
    setEnabled(item ? item.enabled : true);
  }, [target, kind]);

  const save = async () => {
    if (busy) return;
    const found = validateTemplate({ name, description, content });
    if (found) {
      toast.error(t("adminConversation.library.saveFailed"), { description: problemText(t, found) });
      return;
    }
    const normalized = normalizeTemplateName(name);
    const body: Record<string, unknown> = { title: normalized, trigger: normalized, description: description.trim(), enabled, sortOrder: existing?.sortOrder ?? 0 };
    body[kind === "skills" ? "markdown" : "content"] = content.trim();
    setBusy(true);
    try {
      if (existing) await updateLibrary(kind, existing.id, body);
      else await createLibrary(kind, body);
      toast.success(t("adminConversation.library.saved"));
      onSaved();
      onOpenChange(false);
    } catch (error) {
      toast.error(t("adminConversation.library.saveFailed"), { description: message(error) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={target !== null} onOpenChange={(open) => !busy && onOpenChange(open)}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{existing ? t(`adminConversation.library.edit.${kind}`) : t(`adminConversation.library.new.${kind}`)}</DialogTitle>
          <DialogDescription>{t("adminConversation.library.templateHint")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <Field required>
            <FieldLabel>{t("adminConversation.library.fields.name")}</FieldLabel>
            <Input value={name} maxLength={LIMITS.name + 8} onChange={(e) => setName(e.target.value)} placeholder="summarize" autoComplete="off" />
            <FieldHint>{t("adminConversation.library.fields.nameHint")}</FieldHint>
          </Field>
          <Field>
            <FieldLabel>{t("adminConversation.library.fields.description")}</FieldLabel>
            <Input value={description} onChange={(e) => setDescription(e.target.value)} />
          </Field>
          <Field required>
            <FieldLabel>{kind === "skills" ? t("adminConversation.library.fields.markdown") : t("adminConversation.library.fields.content")}</FieldLabel>
            <Textarea rows={10} value={content} onChange={(e) => setContent(e.target.value)} className="font-mono text-[13px]" />
            <FieldHint>{t("adminConversation.library.fields.contentCount", { count: [...content.trim()].length, max: LIMITS.content })}</FieldHint>
          </Field>
          <label className="flex items-center gap-3 text-sm font-medium">
            <Switch checked={enabled} onCheckedChange={setEnabled} />
            {t("adminConversation.library.columns.enabled")}
          </label>
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={busy} onClick={save}>
            {t("common.actions.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function ComponentDialog({ target, onOpenChange, onSaved }: { target: ComponentItem | "new" | null; onOpenChange: (open: boolean) => void; onSaved: () => void }) {
  const t = useT();
  const message = useErrorMessage();
  const shown = useSnapshot(target);
  const existing = shown && shown !== "new" ? shown : null;
  const builtin = existing?.scope === "builtin";
  const [draft, setDraft] = useState({ name: "", description: "", propsSummary: "", propsSchema: "", rendererSource: "", enabled: true });
  const [sample, setSample] = useState("{}");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (target === null) return;
    const item = target === "new" ? null : target;
    setDraft({
      name: item?.name ?? "",
      description: item?.description ?? "",
      propsSummary: item?.propsSummary ?? "",
      propsSchema: item?.propsSchema ?? "",
      rendererSource: item?.rendererSource ?? "",
      enabled: item ? item.enabled : true,
    });
    setSample("{}");
  }, [target]);

  const sampleProps = parseSampleProps(sample);
  const set = <K extends keyof typeof draft>(key: K, value: (typeof draft)[K]) => setDraft((d) => ({ ...d, [key]: value }));

  const save = async () => {
    if (busy) return;
    if (!builtin) {
      const found = validateComponent(draft);
      if (found) {
        toast.error(t("adminConversation.library.saveFailed"), { description: problemText(t, found) });
        return;
      }
    }
    // Built-in components accept only the enabled flag.
    const body: Record<string, unknown> = builtin
      ? { enabled: draft.enabled }
      : { name: draft.name.trim(), description: draft.description.trim(), propsSummary: draft.propsSummary.trim(), propsSchema: draft.propsSchema.trim(), rendererSource: draft.rendererSource, enabled: draft.enabled, sortOrder: existing?.sortOrder ?? 0 };
    setBusy(true);
    try {
      if (existing) await updateLibrary("components", existing.id, body);
      else await createLibrary("components", body);
      toast.success(t("adminConversation.library.saved"));
      onSaved();
      onOpenChange(false);
    } catch (error) {
      toast.error(t("adminConversation.library.saveFailed"), { description: message(error) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={target !== null} onOpenChange={(open) => !busy && onOpenChange(open)}>
      <DialogContent className="max-w-5xl">
        <DialogHeader>
          <DialogTitle>{existing ? t("adminConversation.library.edit.components") : t("adminConversation.library.new.components")}</DialogTitle>
          <DialogDescription>{builtin ? t("adminConversation.library.builtinNote") : t("adminConversation.library.componentHint")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-6 lg:grid-cols-2">
          <div className="grid content-start gap-4">
            <Field required>
              <FieldLabel>{t("adminConversation.library.fields.componentName")}</FieldLabel>
              <Input value={draft.name} readOnly={builtin} onChange={(e) => set("name", e.target.value)} placeholder="price-card" autoComplete="off" spellCheck={false} />
              <FieldHint>{t("adminConversation.library.fields.componentNameHint")}</FieldHint>
            </Field>
            <Field required>
              <FieldLabel>{t("adminConversation.library.fields.description")}</FieldLabel>
              <Input value={draft.description} readOnly={builtin} onChange={(e) => set("description", e.target.value)} />
            </Field>
            <Field required>
              <FieldLabel>{t("adminConversation.library.fields.propsSummary")}</FieldLabel>
              <Textarea rows={3} value={draft.propsSummary} readOnly={builtin} onChange={(e) => set("propsSummary", e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>{t("adminConversation.library.fields.propsSchema")}</FieldLabel>
              <Textarea rows={5} value={draft.propsSchema} readOnly={builtin} spellCheck={false} onChange={(e) => set("propsSchema", e.target.value)} className="font-mono text-[12.5px]" />
              <FieldHint>{t("adminConversation.library.fields.propsSchemaHint")}</FieldHint>
            </Field>
            <label className="flex items-center gap-3 text-sm font-medium">
              <Switch checked={draft.enabled} onCheckedChange={(on) => set("enabled", on)} />
              {t("adminConversation.library.columns.enabled")}
            </label>
          </div>
          <div className="grid content-start gap-4">
            <Field required>
              <FieldLabel>{t("adminConversation.library.fields.rendererSource")}</FieldLabel>
              <Textarea rows={9} value={draft.rendererSource} readOnly={builtin} spellCheck={false} onChange={(e) => set("rendererSource", e.target.value)} className="font-mono text-[12.5px]" />
            </Field>
            {!builtin ? (
              <>
                <Field invalid={sampleProps === null}>
                  <FieldLabel>{t("adminConversation.library.fields.sampleProps")}</FieldLabel>
                  <Textarea rows={3} value={sample} spellCheck={false} onChange={(e) => setSample(e.target.value)} className="font-mono text-[12.5px]" />
                  <FieldError>{sampleProps === null ? t("adminConversation.library.fields.sampleInvalid") : null}</FieldError>
                </Field>
                {sampleProps !== null && draft.rendererSource.trim() ? (
                  <ComponentPreview source={draft.rendererSource} props={sampleProps} title={t("adminConversation.library.fields.preview")} />
                ) : (
                  <p className="rounded-lg border border-dashed p-4 text-sm text-muted-foreground">{t("adminConversation.library.fields.previewOff")}</p>
                )}
              </>
            ) : null}
          </div>
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={busy} onClick={save}>
            {t("common.actions.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
