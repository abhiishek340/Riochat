"use client";

import { Activity, ArrowDownUp, ChevronRight, Layers, ListTree, MoreHorizontal, Pencil, Plus, Trash2, Zap } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { cn } from "@/lib/utils/cn";
import { BulkBar } from "../components/bulk-bar";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { ModelIcon } from "../components/model-icon";
import { AdminPage, Section } from "../components/page";
import { Pagination } from "../components/pagination";
import { ProbeDialog } from "../components/probe-dialog";
import { AdminSection } from "../components/section-guard";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { batchDeleteInChunks, failedIds, splitBatch } from "../lib/bulk";
import { formatDateTime } from "../lib/format";
import { kindsForProtocols, kindsJson, parseKinds, parseStringList, protocolLabel, PROTOCOLS } from "../lib/protocols";
import { invalidateModels } from "../lib/reference";
import { useBulk } from "../lib/use-bulk";
import { useNotify } from "../lib/use-notify";
import { usePagedQuery } from "../lib/use-paged-query";
import { useRowUpdates } from "../lib/use-row-updates";
import { useSelection } from "../lib/use-selection";
import type { PlatformModel } from "../lib/wire";
import { batchDeleteModels, assignDisplayGroup, deleteModel, listDisplayGroups, listModels, listVendors, probeModel, probeModelAll, setModelProtocols, updateModel, deleteRoute, type BatchResult, type DisplayGroup, type ProbeResult, type Vendor } from "../llm-api";
import { loadBreaker, saveBreaker, type BreakerSetting } from "./api";
import { BulkDialog, type BulkAction, type BulkValue } from "./bulk-dialog";
import { parseCaps, stringifyCaps } from "./capabilities";
import { windowOf, writeCustomWindow } from "./context";
import { ModelSheet } from "./model-sheet";
import { OrderSheet } from "./order-sheet";
import { PresentationDialog } from "./presentation-dialog";
import { breakerEnabled, leavesList, protocolTargets, reloadsAfterToggle, sourceBadge, withStatus } from "./rules";
import { SourcesSheet } from "./sources-sheet";
import { SourcesTable } from "./sources-table";
import { useSources } from "./use-sources";

const SORTS = ["sortOrder_asc", "updated_desc", "id_desc", "platformModelName_asc", "sourceCount_desc"] as const;

type Confirm = { items: PlatformModel[] } | null;

export function AdminModelsPage() {
  return (
    <AdminSection>
      <ModelsContent />
    </AdminSection>
  );
}

function InlineSources({ model, breakerOn, onModel, onProbe, onChanged }: { model: PlatformModel; breakerOn: boolean; onModel: (id: number, change: (m: PlatformModel) => PlatformModel) => void; onProbe: (r: ProbeResult[]) => void; onChanged: () => void }) {
  const ctl = useSources({ model, enabled: true, breakerOn, pageSize: 10, onModel: (change) => onModel(model.id, change), onChanged });
  return <SourcesTable model={model} ctl={ctl} breakerOn={breakerOn} onProbe={onProbe} compact />;
}

function ModelsContent() {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const message = useErrorMessage();
  const [q, setQ] = useState("");
  const [status, setStatus] = useState("");
  const [protocol, setProtocol] = useState("");
  const [vendor, setVendor] = useState("");
  const [sort, setSort] = useState<string>("sortOrder_asc");
  const [vendors, setVendors] = useState<Vendor[]>([]);
  const [groups, setGroups] = useState<DisplayGroup[]>([]);
  const [breaker, setBreaker] = useState<BreakerSetting | null>(null);
  const [breakerBusy, setBreakerBusy] = useState(false);
  const [editing, setEditing] = useState<PlatformModel | "new" | null>(null);
  const [sourcesOf, setSourcesOf] = useState<PlatformModel | null>(null);
  const [orderOpen, setOrderOpen] = useState(false);
  const [presentationOpen, setPresentationOpen] = useState(false);
  const [confirm, setConfirm] = useState<Confirm>(null);
  const [bulkAction, setBulkAction] = useState<BulkAction | null>(null);
  const [expanded, setExpanded] = useState<ReadonlySet<number>>(new Set());
  const [probes, setProbes] = useState<ProbeResult[] | null>(null);
  const [probeTitle, setProbeTitle] = useState<string | undefined>();
  const [testing, setTesting] = useState<number | null>(null);
  const selection = useSelection<number>();
  const filters = useMemo(() => ({ q, status, protocol, vendor, sort }), [q, status, protocol, vendor, sort]);
  const breakerOn = breaker?.exists === true && breakerEnabled(breaker.value);

  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) => listModels({ page, pageSize, upstream: "", ...f }, signal),
    onError: (error) => notify.error("adminModels.toast.loadFailed", error),
  });
  // A fresh answer starts a new selection; local edits (a failed bulk run narrows it) keep it.
  useEffect(() => {
    selection.clear();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [list.loadId]);

  const loadPresentation = useCallback(() => {
    listVendors().then(setVendors, (e: unknown) => notify.error("adminModels.toast.loadFailed", e));
    listDisplayGroups().then(setGroups, (e: unknown) => notify.error("adminModels.toast.loadFailed", e));
  }, [notify]);
  useEffect(() => {
    loadPresentation();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  useEffect(() => {
    loadBreaker().then(setBreaker, () => setBreaker({ exists: false, value: "" }));
  }, []);

  const getKey = useCallback((m: PlatformModel) => m.id, []);
  const rows = useRowUpdates<PlatformModel, number>({ getKey, setRows: list.setRows });
  const bulk = useBulk<PlatformModel, number>({ getKey, setRows: list.setRows, selection });
  const selected = list.rows.filter((m) => selection.has(m.id));
  const patchModel = useCallback((id: number, change: (m: PlatformModel) => PlatformModel) => list.setRows((r) => r.map((m) => (m.id === id ? change(m) : m))), [list]);
  // The sources sheet follows the list row so counts stay in step.
  const sheetModel = sourcesOf ? (list.rows.find((m) => m.id === sourcesOf.id) ?? sourcesOf) : null;

  const toggleBreaker = async () => {
    if (!breaker?.exists || breakerBusy) return;
    setBreakerBusy(true);
    try {
      const value = await saveBreaker(breaker.value, !breakerOn);
      setBreaker({ exists: true, value });
      invalidateModels();
      toast.success(t(breakerEnabled(value) ? "adminModels.breaker.enabledToast" : "adminModels.breaker.disabledToast"));
    } catch (error) {
      notify.error("adminModels.breaker.saveFailed", error);
    } finally {
      setBreakerBusy(false);
    }
  };

  const toggleStatus = async (m: PlatformModel, on: boolean) => {
    const next = on ? "active" : "inactive";
    if (leavesList(status, next)) {
      // The row no longer belongs in this filtered list.
      if (rows.isPending(m.id)) return;
      list.setRows((r) => r.filter((x) => x.id !== m.id));
      list.setTotal((n) => n - 1);
      try {
        await updateModel(m.id, { status: next });
        invalidateModels();
        if (list.rows.length <= 1 && list.page > 1) list.setPage(list.page - 1);
        else list.reload();
      } catch (error) {
        notify.error("adminModels.toast.updateFailed", error);
        list.reload();
      }
      return;
    }
    void rows.update(m, (r) => withStatus(r, next), () => updateModel(m.id, { status: next }), {
      failKey: "adminModels.toast.updateFailed",
      onSuccess: () => {
        invalidateModels();
        if (reloadsAfterToggle(sort)) list.reload();
      },
    });
  };

  const toggleScope = (m: PlatformModel) => {
    const next = m.accessScope === "internal" ? "public" : "internal";
    void rows.update(m, (r) => ({ ...r, accessScope: next }), () => updateModel(m.id, { accessScope: next }), {
      failKey: "adminModels.toast.updateFailed",
      onSuccess: () => {
        invalidateModels();
        toast.success(t("adminModels.toast.scopeChanged", { name: m.platformModelName, scope: t(`adminModels.scope.${next}`) }));
        if (reloadsAfterToggle(sort)) list.reload();
      },
    });
  };

  const runTest = async (m: PlatformModel, all: boolean) => {
    setTesting(m.id);
    try {
      if (all) {
        const out = await probeModelAll(m.id);
        if (out.results.length === 0) {
          toast.info(t("adminModels.test.none"));
          return;
        }
        toast.info(t("adminModels.test.summary", { ok: out.successCount, failed: out.failedCount, unsupported: out.unsupportedCount }));
        setProbeTitle(t("adminModels.test.allTitle", { name: m.platformModelName }));
        setProbes(out.results);
      } else {
        const result = await probeModel(m.id);
        setProbeTitle(t("adminModels.test.title", { name: m.platformModelName }));
        setProbes([result]);
      }
    } catch (error) {
      notify.error("adminModels.test.failed", error);
    } finally {
      setTesting(null);
    }
  };

  const runDelete = async () => {
    if (!confirm) return;
    const ids = confirm.items.map((m) => m.id);
    let result: BatchResult;
    try {
      if (ids.length === 1) {
        await deleteModel(ids[0] as number);
        result = { total: 1, successCount: 1, notFoundCount: 0, failedCount: 0, results: [{ id: ids[0] as number, status: "deleted" }] };
      } else {
        result = await batchDeleteInChunks(ids, batchDeleteModels);
      }
    } catch (error) {
      toast.error(t("adminModels.toast.deleteFailed"), { description: message(error) });
      throw error;
    }
    invalidateModels();
    const failed = failedIds(result);
    if (failed.size > 0) {
      // Rows that are gone leave the list; the ones the server kept stay selected for another try.
      const gone = new Set(splitBatch(ids, result).gone);
      list.setRows((r) => r.filter((m) => !gone.has(m.id)));
      list.setTotal((n) => n - gone.size);
      selection.set(failed);
      toast.error(t("adminCommon.toast.batchDeletePartial", { deleted: result.successCount, missing: result.notFoundCount, failed: result.failedCount }));
      return;
    }
    toast.success(ids.length === 1 ? t("adminModels.toast.deleted", { count: 1 }) : t("adminCommon.toast.batchDeleted", { deleted: result.successCount, missing: result.notFoundCount }));
    selection.clear();
    if (ids.length >= list.rows.length && list.page > 1) list.setPage(list.page - 1);
    else list.reload();
  };

  const applyBulk = async (v: BulkValue): Promise<boolean> => {
    const done = (ok: boolean) => {
      if (ok) {
        invalidateModels();
        list.reload();
      }
      return ok;
    };
    switch (v.action) {
      case "status":
        return done(await bulk.run({ targets: selected, alreadyDone: (m) => m.status === v.status, optimistic: (m) => withStatus(m, v.status), request: (m) => updateModel(m.id, { status: v.status }), successKey: "adminCommon.toast.bulkDone" }));
      case "kinds": {
        const json = kindsJson(v.kinds);
        return done(await bulk.run({ targets: selected, alreadyDone: (m) => kindsJson(parseKinds(m.kindsJSON)) === json, optimistic: (m) => ({ ...m, kindsJSON: json }), request: (m) => updateModel(m.id, { kindsJSON: json }), successKey: "adminCommon.toast.bulkDone" }));
      }
      case "protocol": {
        const targets = protocolTargets(selected);
        if (targets.length === 0) {
          notify.info("adminModels.bulk.noSources");
          return false;
        }
        const json = kindsJson(kindsForProtocols([v.protocol]));
        return done(
          await bulk.run({
            targets,
            alreadyDone: () => false,
            optimistic: (m) => ({ ...m, protocolsJSON: JSON.stringify([v.protocol]), kindsJSON: json }),
            request: async (m) => {
              await setModelProtocols(m.id, [v.protocol], json);
            },
            successKey: "adminCommon.toast.bulkDone",
          }),
        );
      }
      case "vendor":
        return done(await bulk.run({ targets: selected, alreadyDone: (m) => m.vendor === v.vendor, optimistic: (m) => ({ ...m, vendor: v.vendor }), request: (m) => updateModel(m.id, { vendor: v.vendor }), successKey: "adminCommon.toast.bulkDone" }));
      case "group":
        try {
          await assignDisplayGroup(selected.map((m) => m.id), v.groupId);
          toast.success(t("adminCommon.toast.bulkDone", { count: selected.length }));
          selection.clear();
          return done(true);
        } catch (error) {
          notify.error("adminCommon.toast.bulkFailed", error);
          return false;
        }
      case "window":
        return done(
          await bulk.run({
            targets: selected,
            alreadyDone: (m) => {
              const parsed = parseCaps(m.capabilitiesJSON);
              const state = parsed.ok ? windowOf(parsed.value) : null;
              return state?.mode === "custom" && state.value === v.window;
            },
            optimistic: (m) => m,
            request: (m) => {
              const parsed = parseCaps(m.capabilitiesJSON);
              if (!parsed.ok) return Promise.reject(new Error(t("adminModels.bulk.problems.notObject", { model: m.platformModelName })));
              return updateModel(m.id, { capabilitiesJSON: stringifyCaps(writeCustomWindow(parsed.value, v.window)) });
            },
            successKey: "adminCommon.toast.bulkDone",
          }),
        );
    }
  };

  const columns: Column<PlatformModel>[] = [
    {
      id: "model",
      sticky: "left",
      header: t("adminModels.columns.model"),
      className: "min-w-52",
      cell: (m) => (
        <div className="flex min-w-0 items-center gap-2.5">
          <ModelIcon icon={m.icon || m.vendorIcon} name={m.platformModelName} size={24} />
          <div className="min-w-0">
            <div className="truncate font-medium">{m.platformModelName}</div>
            {m.displayGroupName ? <div className="truncate text-xs text-muted-foreground">{m.displayGroupName}</div> : null}
          </div>
        </div>
      ),
    },
    {
      id: "kinds",
      header: t("adminModels.columns.kinds"),
      cell: (m) => {
        const kinds = parseKinds(m.kindsJSON);
        return kinds.length === 0 ? (
          <span className="text-muted-foreground">-</span>
        ) : (
          <div className="flex flex-wrap gap-1">
            {kinds.map((k) => (
              <Badge key={k} tone="outline">
                {t(`adminModels.kinds.${k}`)}
              </Badge>
            ))}
          </div>
        );
      },
    },
    {
      id: "protocols",
      header: t("adminModels.columns.protocols"),
      className: "max-w-[16rem]",
      cell: (m) => {
        const protocols = parseStringList(m.protocolsJSON);
        if (protocols.length === 0) return <span className="text-muted-foreground">-</span>;
        return (
          <div className="flex flex-wrap gap-1">
            {protocols.slice(0, 2).map((p) => (
              <Badge key={p} tone="neutral" title={p}>
                {protocolLabel(p)}
              </Badge>
            ))}
            {protocols.length > 2 ? <Badge tone="outline">{t("adminCommon.common.more", { count: protocols.length - 2 })}</Badge> : null}
          </div>
        );
      },
    },
    { id: "vendor", header: t("adminModels.columns.vendor"), cell: (m) => m.vendorName || m.vendor || "-" },
    {
      id: "sources",
      header: t("adminModels.columns.sources"),
      cell: (m) => {
        const badge = sourceBadge(m);
        const open = expanded.has(m.id);
        return (
          <div className="flex items-center gap-2">
            <button
              type="button"
              aria-expanded={open}
              disabled={badge === "none"}
              aria-label={`${t("adminModels.actions.toggleSources")}: ${m.platformModelName}`}
              onClick={() => setExpanded((s) => {
                const next = new Set(s);
                if (open) next.delete(m.id);
                else next.add(m.id);
                return next;
              })}
              className="inline-flex items-center gap-1 rounded px-1 tabular-nums underline-offset-2 enabled:hover:underline disabled:cursor-default"
            >
              <ChevronRight className={cn("size-3.5 transition-transform", open && "rotate-90", badge === "none" && "opacity-30")} />
              {m.activeSourceCount} / {m.sourceCount}
            </button>
            {badge === "none" ? <Badge tone="warning">{t("adminModels.sources.noSource")}</Badge> : null}
            {badge === "notEnabled" ? <Badge tone="warning">{t("adminModels.sources.notEnabled")}</Badge> : null}
          </div>
        );
      },
    },
    {
      id: "status",
      header: t("adminModels.columns.status"),
      cell: (m) => <Switch checked={m.status === "active"} disabled={rows.isPending(m.id)} aria-label={`${m.platformModelName}: ${t("adminModels.columns.status")}`} onCheckedChange={(on) => void toggleStatus(m, on)} />,
    },
    {
      id: "scope",
      header: t("adminModels.columns.scope"),
      cell: (m) => (
        <button type="button" disabled={rows.isPending(m.id)} aria-label={`${m.platformModelName}: ${t("adminModels.columns.scope")}`} onClick={() => toggleScope(m)} className="rounded-full focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-60">
          <Badge tone={m.accessScope === "internal" ? "warning" : "success"}>{t(`adminModels.scope.${m.accessScope === "internal" ? "internal" : "public"}`)}</Badge>
        </button>
      ),
    },
    { id: "updated", header: t("adminModels.columns.updated"), className: "whitespace-nowrap text-[13px] text-muted-foreground", cell: (m) => formatDateTime(m.updatedAt, locale) },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (m) => (
        <CellActions>
          <Button variant="ghost" size="icon-sm" aria-label={`${t("adminModels.actions.edit")}: ${m.platformModelName}`} onClick={() => setEditing(m)}>
            <Pencil />
          </Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="ghost" size="icon-sm" aria-label={`${t("adminCommon.table.moreActions")}: ${m.platformModelName}`}>
                <MoreHorizontal />
              </Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent>
              <DropdownMenuItem disabled={testing === m.id || m.sourceCount === 0} onSelect={() => void runTest(m, false)}>
                <Activity />
                {t("adminModels.actions.test")}
              </DropdownMenuItem>
              <DropdownMenuItem disabled={testing === m.id || m.sourceCount === 0} onSelect={() => void runTest(m, true)}>
                <Zap />
                {t("adminModels.actions.testAll")}
              </DropdownMenuItem>
              <DropdownMenuItem onSelect={() => setSourcesOf(m)}>
                <ListTree />
                {t("adminModels.actions.sources")}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem destructive onSelect={() => setConfirm({ items: [m] })}>
                <Trash2 />
                {t("common.actions.delete")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </CellActions>
      ),
    },
  ];

  const protocolOptions = [{ value: "", label: t("adminModels.filters.anyProtocol") }, ...PROTOCOLS.map((p) => ({ value: p, label: protocolLabel(p) }))];
  const vendorOptions = [{ value: "", label: t("adminModels.filters.anyVendor") }, ...vendors.map((v) => ({ value: v.key, label: v.name }))];

  return (
    <AdminPage
      title={t("adminModels.title")}
      description={t("adminModels.description")}
      actions={
        <>
          <button
            type="button"
            role="switch"
            aria-checked={breakerOn}
            disabled={!breaker?.exists || breakerBusy}
            onClick={toggleBreaker}
            title={t("adminModels.breaker.pillHint")}
            className={cn("inline-flex h-9 items-center gap-2 rounded-full border px-3 text-sm font-medium transition-colors focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-60", breakerOn ? "border-success/40 bg-success/10 text-success" : "text-muted-foreground")}
          >
            <Zap className="size-4" />
            {t(breakerOn ? "adminModels.breaker.pillOn" : "adminModels.breaker.pillOff")}
          </button>
          <Button variant="outline" onClick={() => setPresentationOpen(true)}>
            <Layers />
            {t("adminModels.presentation.manage")}
          </Button>
          <Button variant="outline" onClick={() => setOrderOpen(true)}>
            <ArrowDownUp />
            {t("adminModels.order.open")}
          </Button>
          <Button onClick={() => setEditing("new")}>
            <Plus />
            {t("adminModels.create")}
          </Button>
        </>
      }
    >
      <Section flush>
        <div className="space-y-3 p-4">
          <Toolbar>
            <SearchBox value={q} onChange={setQ} placeholder={t("adminModels.filters.search")} className="sm:w-72" />
            <OptionSelect value={status} onChange={setStatus} label={t("adminModels.columns.status")} options={[{ value: "", label: t("adminModels.filters.anyStatus") }, { value: "active", label: t("adminModels.status.active") }, { value: "inactive", label: t("adminModels.status.inactive") }]} />
            <OptionSelect value={protocol} onChange={setProtocol} label={t("adminModels.columns.protocols")} options={protocolOptions} />
            <OptionSelect value={vendor} onChange={setVendor} label={t("adminModels.columns.vendor")} options={vendorOptions} />
            <OptionSelect value={sort} onChange={setSort} label={t("adminModels.filters.sort")} options={SORTS.map((s) => ({ value: s, label: t(`adminModels.sorts.${s}`) }))} />
          </Toolbar>
          <BulkBar count={selection.count} onClear={selection.clear}>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button size="sm" variant="outline" disabled={bulk.pending}>
                  {t("adminModels.bulk.menu")}
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start">
                {(["status", "kinds", "protocol", "vendor", "group", "window"] as const).map((a) => (
                  <DropdownMenuItem key={a} onSelect={() => setBulkAction(a)}>
                    {t(`adminModels.bulk.items.${a}`)}
                  </DropdownMenuItem>
                ))}
              </DropdownMenuContent>
            </DropdownMenu>
            <Button size="sm" variant="destructive" onClick={() => setConfirm({ items: selected })}>
              {t("common.actions.delete")}
            </Button>
          </BulkBar>
          <DataTable
            columns={columns}
            rows={list.rows}
            rowKey={getKey}
            loading={list.loading} error={list.error} onRetry={list.reload}
            fetching={list.fetching}
            selection={selection}
            empty={t("adminModels.empty")}
            rowLabel={(m) => m.platformModelName} label={t("adminModels.title")}
            renderExpanded={(m) =>
              expanded.has(m.id) && m.sourceCount > 0 ? (
                <InlineSources
                  model={m}
                  breakerOn={breakerOn}
                  onModel={patchModel}
                  onProbe={(r) => {
                    setProbeTitle(t("adminModels.test.title", { name: m.platformModelName }));
                    setProbes(r);
                  }}
                  onChanged={() => list.reload()}
                />
              ) : null
            }
          />
          <Pagination page={list.page} pageSize={list.pageSize} total={list.total} onPage={list.setPage} onPageSize={list.setPageSize} disabled={list.fetching} />
        </div>
      </Section>

      <ModelSheet
        target={editing}
        onOpenChange={(open) => !open && setEditing(null)}
        vendors={vendors}
        groups={groups}
        onManagePresentation={() => setPresentationOpen(true)}
        onManageSources={(m) => {
          setEditing(null);
          setSourcesOf(m);
        }}
        onSaved={(saved, created) => {
          invalidateModels();
          if (!created) list.setRows((r) => r.map((x) => (x.id === saved.id ? saved : x)));
          list.reload();
        }}
      />
      <SourcesSheet model={sheetModel} breakerOn={breakerOn} onClose={() => setSourcesOf(null)} onModel={patchModel} onChanged={() => list.reload()} />
      <OrderSheet open={orderOpen} onOpenChange={setOrderOpen} onSaved={() => list.reload()} />
      <PresentationDialog
        open={presentationOpen}
        onOpenChange={setPresentationOpen}
        vendors={vendors}
        groups={groups}
        onChanged={() => {
          loadPresentation();
          list.reload();
        }}
      />
      <BulkDialog action={bulkAction} models={selected} vendors={vendors} groups={groups} onClose={() => setBulkAction(null)} onApply={applyBulk} />
      <ProbeDialog
        results={probes}
        title={probeTitle}
        onClose={() => setProbes(null)}
        onDeleteRoute={async (r) => {
          await deleteRoute(r.upstreamID, r.routeID);
          invalidateModels();
          list.reload();
        }}
      />
      <ConfirmDialog
        open={confirm !== null}
        onOpenChange={(open) => !open && setConfirm(null)}
        title={t("adminModels.confirm.deleteTitle", { count: confirm?.items.length ?? 0 })}
        description={confirm && confirm.items.length === 1 ? t("adminModels.confirm.deleteOne", { name: confirm.items[0]?.platformModelName ?? "" }) : t("adminModels.confirm.deleteBody")}
        confirmLabel={t("common.actions.delete")}
        destructive
        onConfirm={runDelete}
      />
    </AdminPage>
  );
}
