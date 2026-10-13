"use client";

import { FolderOpen, MoreHorizontal, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Switch } from "@/components/ui/switch";
import { Badge } from "@/components/ui/badge";
import { isAbortError } from "@/lib/api/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useFeaturePolicy } from "@/lib/session/feature-policy";
import { BulkBar } from "../components/bulk-bar";
import { Checkbox } from "../components/checkbox";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { AdminPage, Section, SettingRow } from "../components/page";
import { Pagination } from "../components/pagination";
import { AdminSection } from "../components/section-guard";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { runBulk } from "../lib/bulk";
import { formatDateTime } from "../lib/format";
import { useNotify } from "../lib/use-notify";
import { usePagedQuery } from "../lib/use-paged-query";
import { useSelection } from "../lib/use-selection";
import { deleteKnowledgeBase, listKnowledgeBases, readKbEnabled, writeKbEnabled, type KbSort, type KnowledgeBase } from "./api";
import { KbDetailSheet } from "./kb-detail";
import { KbFormDialog } from "./kb-form";
import { PoolDialog } from "./pool-dialog";
import { readyRatio } from "./rules";

const SORTS: KbSort[] = ["", "updated", "created", "name", "files"];

export function AdminKnowledgeBasesPage() {
  return (
    <AdminSection>
      <KnowledgeBasesContent />
    </AdminSection>
  );
}

function KnowledgeBasesContent() {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const policy = useFeaturePolicy();
  const [enabled, setEnabled] = useState<boolean | null>(null);
  const [q, setQ] = useState("");
  const [sort, setSort] = useState<KbSort>("");
  const [form, setForm] = useState<KnowledgeBase | "new" | null>(null);
  const [detail, setDetail] = useState<KnowledgeBase | null>(null);
  const [pool, setPool] = useState(false);
  const [removing, setRemoving] = useState<KnowledgeBase[] | null>(null);
  const [deleteFiles, setDeleteFiles] = useState(false);
  const selection = useSelection<string>();
  const filters = useMemo(() => ({ q, sort }), [q, sort]);

  useEffect(() => {
    const controller = new AbortController();
    readKbEnabled(controller.signal).then(setEnabled, (error: unknown) => {
      if (!isAbortError(error)) setEnabled(null);
    });
    return () => controller.abort();
  }, []);

  const toggleGlobal = async (next: boolean) => {
    const before = enabled;
    setEnabled(next);
    policy.patch({ knowledgeBaseEnabled: next });
    try {
      await writeKbEnabled(next);
      notify.success(next ? "adminKb.toast.switchedOn" : "adminKb.toast.switchedOff");
    } catch (error) {
      setEnabled(before);
      policy.patch({ knowledgeBaseEnabled: before ?? true });
      notify.error("adminKb.toast.switchFailed", error);
    }
  };

  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) => listKnowledgeBases({ page, pageSize, ...f }, signal),
    onError: (error) => notify.error("adminKb.toast.loadFailed", error),
  });
  // A fresh answer starts a new selection; local edits (a failed bulk run narrows it) keep it.
  useEffect(() => {
    selection.clear();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [list.loadId]);

  const confirmDelete = async () => {
    if (!removing) return;
    const outcome = await runBulk(removing, (kb) => deleteKnowledgeBase(kb.publicID, deleteFiles), { chunkSize: 10, sequential: true });
    if (outcome.failed.length > 0) notify.error("adminKb.toast.deleteFailed", outcome.failed[0]?.error);
    if (outcome.succeeded.length > 0) notify.success("adminKb.toast.deleted", { count: outcome.succeeded.length });
    selection.clear();
    if (outcome.succeeded.length >= list.rows.length && list.page > 1) list.setPage(list.page - 1);
    else list.reload();
  };

  const columns: Column<KnowledgeBase>[] = [
    {
      id: "name",
      header: t("adminKb.columns.name"),
      sticky: "left",
      className: "max-w-[22rem]",
      cell: (kb) => (
        <div className="min-w-0">
          <div className="truncate font-medium">{kb.name}</div>
          <div className="truncate text-xs text-muted-foreground">{kb.description || t("adminKb.defaultDescription")}</div>
        </div>
      ),
    },
    { id: "files", header: t("adminKb.columns.files"), className: "tabular-nums", cell: (kb) => readyRatio(kb) },
    { id: "status", header: t("adminKb.columns.status"), cell: (kb) => <Badge tone={kb.enabled ? "success" : "neutral"}>{kb.enabled ? t("adminKb.status.enabled") : t("adminKb.status.disabled")}</Badge> },
    { id: "updated", header: t("adminKb.columns.updated"), className: "whitespace-nowrap text-muted-foreground", cell: (kb) => formatDateTime(kb.updatedAt, locale) },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (kb) => (
        <CellActions>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label={`${t("adminCommon.table.moreActions")}: ${kb.name}`}>
                <MoreHorizontal />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem onSelect={() => setDetail(kb)}>
                <FolderOpen />
                {t("adminKb.actions.manage")}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => setForm(kb)}>
                <Pencil />
                {t("common.actions.edit")}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem destructive onSelect={() => setRemoving([kb])}>
                <Trash2 />
                {t("common.actions.delete")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </CellActions>
      ),
    },
  ];

  return (
    <AdminPage
      title={t("adminKb.title")}
      description={t("adminKb.description")}
      actions={
        <>
          <Button variant="outline" onClick={() => setPool(true)}>
            {t("adminKb.platformFiles")}
          </Button>
          <Button onClick={() => setForm("new")}>
            <Plus />
            {t("adminKb.create")}
          </Button>
        </>
      }
    >
      <Section>
        <SettingRow label={t("adminKb.global.label")} hint={t("adminKb.global.hint")} htmlFor="kb-global">
          <Switch id="kb-global" checked={enabled === true} disabled={enabled === null} onCheckedChange={(on) => void toggleGlobal(on)} />
        </SettingRow>
      </Section>
      <Section title={t("adminKb.list.title")} flush>
        <div className="space-y-3 p-4">
          <Toolbar>
            <SearchBox value={q} onChange={setQ} placeholder={t("adminKb.list.search")} className="sm:w-72" />
            <OptionSelect value={sort} onChange={(v) => setSort(v as KbSort)} label={t("adminKb.sort.label")} options={SORTS.map((s) => ({ value: s, label: t(`adminKb.sort.${s || "default"}`) }))} />
            <Button variant="outline" size="icon" aria-label={t("common.actions.refresh")} loading={list.fetching} onClick={list.reload}>
              <RefreshCw />
            </Button>
          </Toolbar>
          <BulkBar count={selection.count} onClear={selection.clear}>
            <Button size="sm" variant="destructive" onClick={() => setRemoving(list.rows.filter((kb) => selection.has(kb.publicID)))}>
              {t("common.actions.delete")}
            </Button>
          </BulkBar>
          <DataTable
            columns={columns}
            rows={list.rows}
            rowKey={(kb) => kb.publicID}
            loading={list.loading} error={list.error} onRetry={list.reload}
            fetching={list.fetching}
            selection={selection}
            rowTone={(kb) => (kb.enabled ? undefined : "muted")}
            onRowClick={setDetail}
            empty={t("adminKb.list.empty")}
            rowLabel={(kb) => kb.name} label={t("adminKb.list.title")}
          />
          <Pagination page={list.page} pageSize={list.pageSize} total={list.total} onPage={list.setPage} onPageSize={list.setPageSize} disabled={list.fetching} />
        </div>
      </Section>

      <KbFormDialog
        target={form}
        onOpenChange={(open) => !open && setForm(null)}
        onSaved={(saved, created) => {
          if (created) list.reload();
          else {
            list.setRows((rows) => rows.map((r) => (r.publicID === saved.publicID ? saved : r)));
            setDetail((d) => (d && d.publicID === saved.publicID ? saved : d));
          }
        }}
      />
      {detail ? (
        <KbDetailSheet
          key={detail.publicID}
          kb={detail}
          onClose={() => setDetail(null)}
          onChanged={(next) => {
            list.setRows((rows) => rows.map((r) => (r.publicID === next.publicID ? next : r)));
            setDetail((d) => (d ? next : d));
          }}
        />
      ) : null}
      <PoolDialog open={pool} onOpenChange={setPool} />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => {
          if (!open) {
            setRemoving(null);
            setDeleteFiles(false);
          }
        }}
        title={removing && removing.length === 1 ? t("adminKb.confirm.deleteOne", { name: removing[0]?.name ?? "" }) : t("adminKb.confirm.deleteMany", { count: removing?.length ?? 0 })}
        description={
          <span className="grid gap-3">
            <span>{t("adminKb.confirm.deleteBody")}</span>
            <label className="flex items-center gap-2 text-foreground">
              <Checkbox checked={deleteFiles} onCheckedChange={setDeleteFiles} />
              {t("adminKb.confirm.deleteFiles")}
            </label>
          </span>
        }
        confirmLabel={t("common.actions.delete")}
        destructive
        onConfirm={confirmDelete}
      />
    </AdminPage>
  );
}
