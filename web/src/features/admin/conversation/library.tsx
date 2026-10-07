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
