"use client";

import { Download, ListTree, MoreHorizontal, Pencil, Plus, Power, PowerOff, RotateCcw, Trash2, Zap } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { BulkBar } from "../components/bulk-bar";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { Mono, AdminPage, Section } from "../components/page";
import { Pagination } from "../components/pagination";
import { AdminSection } from "../components/section-guard";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { batchDeleteInChunks, failedIds } from "../lib/bulk";
import { formatDateTime, formatNumber } from "../lib/format";
import { COMPATIBLE, COMPATIBLE_LABELS } from "../lib/protocols";
import { invalidateModels } from "../lib/reference";
import { useBulk } from "../lib/use-bulk";
import { useNotify } from "../lib/use-notify";
import { usePagedQuery } from "../lib/use-paged-query";
import { useRowUpdates } from "../lib/use-row-updates";
import { useSelection } from "../lib/use-selection";
import { batchDeleteUpstreams, deleteUpstream, listUpstreams, updateUpstream, upstreamCircuit, type BatchResult as BatchDeleteResult, type Upstream } from "../llm-api";
import { RoutesDialog } from "./routes-dialog";
import { activeModelsShown, defaultCount } from "./rules";
import { SyncDialog } from "./sync-dialog";
import { UpstreamSheet } from "./upstream-sheet";

const SORTS = ["id_desc", "id_asc", "name_asc", "updated_desc"] as const;

type Confirm = { kind: "open" | "reset"; upstream: Upstream } | { kind: "delete"; items: Upstream[] } | null;

export function AdminUpstreamsPage() {
  return (
    <AdminSection>
      <UpstreamsContent />
    </AdminSection>
  );
}

function UpstreamsContent() {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const message = useErrorMessage();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [compatible, setCompatible] = useState("");
  const [sort, setSort] = useState<string>("id_desc");
  const [editing, setEditing] = useState<Upstream | "new" | null>(null);
  const [routesOf, setRoutesOf] = useState<Upstream | null>(null);
  const [syncOf, setSyncOf] = useState<Upstream | null>(null);
  const [confirm, setConfirm] = useState<Confirm>(null);
  const [bulkStatus, setBulkStatus] = useState("active");
  const selection = useSelection<number>();
  const filters = useMemo(() => ({ q, status, compatible, sort }), [q, status, compatible, sort]);

  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) => listUpstreams({ page, pageSize, ...f }, signal),
    onError: (error) => notify.error("adminUpstreams.toast.loadFailed", error),
  });
  // Selection is cleared on every reload.
  // A fresh answer starts a new selection; local edits (a failed bulk run narrows it) keep it.
  useEffect(() => {
    selection.clear();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [list.loadId]);

  const getKey = useCallback((u: Upstream) => u.id, []);
  const rows = useRowUpdates<Upstream, number>({ getKey, setRows: list.setRows });
  const bulk = useBulk<Upstream, number>({ getKey, setRows: list.setRows, selection });
  const selected = list.rows.filter((u) => selection.has(u.id));

  const toggle = (u: Upstream, on: boolean) => {
    const next = on ? "active" : "inactive";
    void rows.update(u, (r) => ({ ...r, status: next }), () => updateUpstream(u.id, { status: next }), { failKey: "adminUpstreams.toast.updateFailed", onSuccess: () => invalidateModels() });
  };

  const runConfirm = async () => {
    if (!confirm) return;
    try {
      if (confirm.kind === "delete") {
        const ids = confirm.items.map((u) => u.id);
        const before = list.rows;
        // The rows leave at once; whatever the server keeps is put back.
        list.setRows((r) => r.filter((u) => !ids.includes(u.id)));
        list.setTotal((n) => n - ids.length);
        let result: BatchDeleteResult;
        try {
          if (ids.length === 1) {
            await deleteUpstream(ids[0] as number);
            result = { total: 1, successCount: 1, notFoundCount: 0, failedCount: 0, results: [{ id: ids[0] as number, status: "deleted" }] };
          } else {
            result = await batchDeleteInChunks(ids, batchDeleteUpstreams);
          }
        } catch (error) {
          list.setRows(() => before);
          list.setTotal((n) => n + ids.length);
          throw error;
        }
        invalidateModels();
        const failed = failedIds(result);
        if (failed.size > 0) {
          list.setRows(() => before.filter((u) => failed.has(u.id) || !ids.includes(u.id)));
          list.setTotal((n) => n + failed.size);
          selection.set(failed);
          toast.error(t("adminCommon.toast.batchDeletePartial", { deleted: result.successCount, missing: result.notFoundCount, failed: result.failedCount }));
          return;
        }
        toast.success(t("adminCommon.toast.batchDeleted", { deleted: result.successCount, missing: result.notFoundCount }));
        selection.clear();
        if (ids.length >= before.length && list.page > 1) list.setPage(list.page - 1);
        else list.reload();
      } else {
        const u = confirm.upstream;
        await upstreamCircuit(u.id, confirm.kind);
        list.setRows((r) => r.map((x) => (x.id === u.id ? { ...x, circuitOpen: confirm.kind === "open" } : x)));
        toast.success(t(confirm.kind === "open" ? "adminUpstreams.toast.circuitOpened" : "adminUpstreams.toast.circuitReset", { name: u.name }));
        invalidateModels();
        list.reload();
      }
    } catch (error) {
      toast.error(t("adminUpstreams.toast.actionFailed"), { description: message(error) });
      throw error;
    }
  };

  const columns: Column<Upstream>[] = [
    {
      id: "name",
      sticky: "left",
      header: t("adminUpstreams.columns.name"),
      cell: (u) => (
        <div className="min-w-0">
          <div className="truncate font-medium">{u.name}</div>
          <div className="truncate text-xs text-muted-foreground">{formatDateTime(u.updatedAt, locale)}</div>
        </div>
      ),
    },
    { id: "url", header: t("adminUpstreams.columns.url"), className: "max-w-[18rem]", cell: (u) => <Mono className="block truncate text-muted-foreground">{u.baseURL}</Mono> },
    { id: "compatible", header: t("adminUpstreams.columns.compatible"), cell: (u) => <Badge tone="outline">{COMPATIBLE_LABELS[u.compatible as (typeof COMPATIBLE)[number]] ?? u.compatible}</Badge> },
    { id: "defaults", header: t("adminUpstreams.columns.defaults"), className: "tabular-nums", cell: (u) => defaultCount(u.protocolDefaultsJSON) || "-" },
    {
      id: "timeouts",
      header: t("adminUpstreams.columns.timeouts"),
      className: "whitespace-nowrap text-[13px] tabular-nums text-muted-foreground",
      cell: (u) => [u.connectTimeoutMS, u.readTimeoutMS, u.streamIdleTimeoutMS].map((n) => (n > 0 ? formatNumber(n, locale) : "-")).join(" / "),
    },
    {
      id: "models",
      header: t("adminUpstreams.columns.models"),
      className: "whitespace-nowrap tabular-nums",
      cell: (u) => (
        <button type="button" className="rounded px-1 underline-offset-2 hover:underline" onClick={() => setRoutesOf(u)}>
          {activeModelsShown(u)} / {u.modelsCount}
        </button>
      ),
    },
    {
      id: "status",
      header: t("adminUpstreams.columns.status"),
      cell: (u) => <Switch checked={u.status === "active"} disabled={rows.isPending(u.id)} aria-label={`${u.name}: ${t("adminUpstreams.columns.status")}`} onCheckedChange={(on) => toggle(u, on)} />,
    },
    {
      id: "circuit",
      header: t("adminUpstreams.columns.circuit"),
      cell: (u) =>
        u.circuitOpen ? (
          <div className="space-y-0.5">
            <Badge tone="danger">{t("adminUpstreams.circuitOpen")}</Badge>
            {u.circuitUntil ? <div className="text-xs text-muted-foreground">{t("adminUpstreams.circuitUntil", { time: formatDateTime(u.circuitUntil, locale) })}</div> : null}
          </div>
        ) : (
          <span className="text-muted-foreground">-</span>
        ),
    },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (u) => (
        <CellActions>
          <Button variant="ghost" size="icon-sm" aria-label={`${t("adminUpstreams.actions.edit")}: ${u.name}`} onClick={() => setEditing(u)}>
            <Pencil />
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label={`${t("adminCommon.table.moreActions")}: ${u.name}`}>
                <MoreHorizontal />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem onSelect={() => setRoutesOf(u)}>
                <ListTree />
                {t("adminUpstreams.actions.manageModels")}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => setSyncOf(u)}>
                <Download />
                {t("adminUpstreams.actions.sync")}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              {u.circuitOpen ? (
                <DropdownMenuItem onSelect={() => setConfirm({ kind: "reset", upstream: u })}>
                  <RotateCcw />
                  {t("adminUpstreams.actions.resetCircuit")}
                </DropdownMenuItem>
              ) : (
                <DropdownMenuItem onSelect={() => setConfirm({ kind: "open", upstream: u })}>
                  <Zap />
                  {t("adminUpstreams.actions.openCircuit")}
                </DropdownMenuItem>
              )}
              <DropdownMenuItem onSelect={() => toggle(u, u.status !== "active")}>
                {u.status === "active" ? <PowerOff /> : <Power />}
                {u.status === "active" ? t("adminUpstreams.actions.disable") : t("adminUpstreams.actions.enable")}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem destructive onSelect={() => setConfirm({ kind: "delete", items: [u] })}>
                <Trash2 />
                {t("common.actions.delete")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </CellActions>
      ),
    },
  ];

  const copy = (() => {
    if (!confirm) return null;
    if (confirm.kind === "delete") return { title: t("adminUpstreams.confirm.deleteTitle", { count: confirm.items.length }), body: t("adminUpstreams.confirm.deleteBody"), action: t("common.actions.delete"), destructive: true };
    if (confirm.kind === "open") return { title: t("adminUpstreams.confirm.openTitle", { name: confirm.upstream.name }), body: t("adminUpstreams.confirm.openBody"), action: t("adminUpstreams.actions.openCircuit"), destructive: true };
    return { title: t("adminUpstreams.confirm.resetTitle", { name: confirm.upstream.name }), body: t("adminUpstreams.confirm.resetBody"), action: t("adminUpstreams.actions.resetCircuit"), destructive: false };
  })();

  return (
    <AdminPage
      title={t("adminUpstreams.title")}
      description={t("adminUpstreams.description")}
      actions={
        <Button onClick={() => setEditing("new")}>
          <Plus />
          {t("adminUpstreams.create")}
        </Button>
      }
    >
      <Section flush>
        <div className="space-y-3 p-4">
          <Toolbar>
            <SearchBox value={q} onChange={setQ} placeholder={t("adminUpstreams.filters.search")} className="sm:w-72" />
            <OptionSelect value={status} onChange={setStatus} label={t("adminUpstreams.columns.status")} options={[{ value: "", label: t("adminUpstreams.filters.anyStatus") }, { value: "active", label: t("adminUpstreams.status.active") }, { value: "inactive", label: t("adminUpstreams.status.inactive") }]} />
            <OptionSelect value={compatible} onChange={setCompatible} label={t("adminUpstreams.columns.compatible")} options={[{ value: "", label: t("adminUpstreams.filters.anyCompatible") }, ...COMPATIBLE.map((c) => ({ value: c, label: COMPATIBLE_LABELS[c] }))]} />
            <OptionSelect value={sort} onChange={setSort} label={t("adminUpstreams.filters.sort")} options={SORTS.map((s) => ({ value: s, label: t(`adminUpstreams.sorts.${s}`) }))} />
          </Toolbar>
          <BulkBar count={selection.count} onClear={selection.clear}>
            <span className="flex items-center gap-1">
              <OptionSelect value={bulkStatus} onChange={setBulkStatus} label={t("adminUpstreams.columns.status")} options={[{ value: "active", label: t("adminUpstreams.status.active") }, { value: "inactive", label: t("adminUpstreams.status.inactive") }]} className="h-8 w-32 sm:min-w-0" />
              <Button
                size="sm"
                variant="outline"
                disabled={bulk.pending}
                onClick={() =>
                  void bulk.run({
                    targets: selected,
                    alreadyDone: (u) => u.status === bulkStatus,
                    optimistic: (u) => ({ ...u, status: bulkStatus }),
                    request: (u) => updateUpstream(u.id, { status: bulkStatus }),
                    successKey: "adminCommon.toast.bulkDone",
                  }).then((ok) => ok && invalidateModels())
                }
              >
                {t("adminUpstreams.actions.applyStatus")}
              </Button>
            </span>
            <Button size="sm" variant="destructive" onClick={() => setConfirm({ kind: "delete", items: selected })}>
              {t("common.actions.delete")}
            </Button>
          </BulkBar>
          <DataTable columns={columns} rows={list.rows} rowKey={getKey} loading={list.loading} error={list.error} onRetry={list.reload} fetching={list.fetching} selection={selection} empty={t("adminUpstreams.empty")} rowLabel={(u) => u.name} label={t("adminUpstreams.title")} />
          <Pagination page={list.page} pageSize={list.pageSize} total={list.total} onPage={list.setPage} onPageSize={list.setPageSize} disabled={list.fetching} />
        </div>
      </Section>

      <UpstreamSheet
        target={editing}
        onOpenChange={(open) => !open && setEditing(null)}
        onManageModels={(u) => {
          setEditing(null);
          setRoutesOf(u);
        }}
        onSaved={(saved, created) => {
          invalidateModels();
          if (created) {
            list.reload();
            setRoutesOf(saved);
          } else {
            list.setRows((r) => r.map((x) => (x.id === saved.id ? saved : x)));
            list.reload();
          }
        }}
      />
      <RoutesDialog upstream={routesOf} onClose={() => setRoutesOf(null)} onChanged={() => list.reload()} />
      <SyncDialog upstream={syncOf} onClose={() => setSyncOf(null)} onChanged={() => list.reload()} />
      <ConfirmDialog open={confirm !== null} onOpenChange={(open) => !open && setConfirm(null)} title={copy?.title ?? ""} description={copy?.body} confirmLabel={copy?.action} destructive={copy?.destructive} onConfirm={runConfirm} />
    </AdminPage>
  );
}
