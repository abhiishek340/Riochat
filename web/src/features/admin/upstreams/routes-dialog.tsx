"use client";

import { Activity, MoreHorizontal, Plus, Power, PowerOff, RotateCcw, Trash2, Zap } from "lucide-react";
import { useCallback, useEffect, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuSeparator, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { BulkBar } from "../components/bulk-bar";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { MultiSelectMenu } from "../components/menu-select";
import { Pagination } from "../components/pagination";
import { ProbeDialog } from "../components/probe-dialog";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { batchDeleteInChunks, failedIds } from "../lib/bulk";
import { invalidateModels } from "../lib/reference";
import { KINDS, protocolLabel, PROTOCOLS, resolveSelection } from "../lib/protocols";
import { useNotify } from "../lib/use-notify";
import { useSelection } from "../lib/use-selection";
import {
  batchDeleteRoutes,
  deleteRoute,
  listRoutes,
  probeRoute,
  routeCircuit,
  setRouteStatus,
  upsertBinding,
  type ProbeResult,
  type Upstream,
} from "../llm-api";
import { groupRoutes, kindsOfRow, newBindingProblem, planSave, rowDirty, validateBindings, type BindingRow } from "./rules";
import { kindsJson } from "../lib/protocols";

const SORTS = ["upstream_asc", "upstream_desc", "platform_asc", "platform_desc", "status_asc", "protocol_asc"] as const;

export function RoutesDialog({ upstream, onClose, onChanged }: { upstream: Upstream | null; onClose: () => void; onChanged: () => void }) {
  const t = useT();
  const message = useErrorMessage();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [routeStatus, setRouteStatusFilter] = useState("bound");
  const [upstreamStatus, setUpstreamStatus] = useState("");
  const [protocol, setProtocol] = useState("");
  const [sort, setSort] = useState("upstream_asc");
  const [page, setPage] = useState(1);
  const [pageSize, setPageSize] = useState(25);
  const [rows, setRows] = useState<BindingRow[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [nonce, setNonce] = useState(0);
  const [probes, setProbes] = useState<ProbeResult[] | null>(null);
  const [adding, setAdding] = useState(false);
  const [removing, setRemoving] = useState<BindingRow[] | null>(null);
  const selection = useSelection<string>();

  const dirty = rows.some(rowDirty);
  const id = upstream?.id;

  useEffect(() => {
    setPage(1);
  }, [q, routeStatus, upstreamStatus, protocol, sort, pageSize]);

  useEffect(() => {
    if (!id) return;
    const controller = new AbortController();
    setLoading(true);
    listRoutes(id, { page, pageSize, q, routeStatus, upstreamStatus, protocol, sort }, controller.signal).then(
      (result) => {
        setRows(groupRoutes(result.results));
        setTotal(result.total);
        selection.clear();
        setLoading(false);
      },
      (error: unknown) => {
        if (isAbortError(error)) return;
        setLoading(false);
        notify.error("adminUpstreams.toast.routesLoadFailed", error);
      },
    );
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, page, pageSize, q, routeStatus, upstreamStatus, protocol, sort, nonce]);

  const reload = useCallback(() => setNonce((n) => n + 1), []);
  const update = (key: string, patch: Partial<BindingRow>) => setRows((list) => list.map((r) => (r.key === key ? { ...r, ...patch } : r)));
  const keyOf = (r: BindingRow) => r.key;

  const save = async () => {
    if (!id) return;
    const problem = validateBindings(rows);
    if (problem) {
      toast.error(t("adminUpstreams.routes.saveFailed"), { description: t(`adminUpstreams.routes.problems.${problem.kind}`) });
      return;
    }
    const plan = planSave(rows);
    if (plan.upserts.length === 0 && plan.deletes.length === 0) {
      toast.info(t("adminUpstreams.routes.noChanges"));
      return;
    }
    setSaving(true);
    try {
      for (const body of plan.upserts) await upsertBinding(id, body);
      const removal = await batchDeleteInChunks(plan.deletes, (group) => batchDeleteRoutes(id, group));
      invalidateModels();
      if (removal.failedCount > 0) {
        // The upserts and the deletes that worked are real; say what is left and show the fresh list.
        toast.error(t("adminUpstreams.routes.saveFailed"), { description: t("adminCommon.toast.batchDeletePartial", { deleted: removal.successCount, missing: removal.notFoundCount, failed: removal.failedCount }) });
        reload();
        onChanged();
        return;
      }
      const parts = [plan.upserts.length > 0 ? t("adminUpstreams.routes.savedCount", { count: plan.upserts.length }) : "", plan.deletes.length > 0 ? t("adminUpstreams.routes.deletedCount", { count: plan.deletes.length }) : ""].filter(Boolean);
      toast.success(parts.join(" · "));
      reload();
      onChanged();
    } catch (error) {
      toast.error(t("adminUpstreams.routes.saveFailed"), { description: message(error) });
    } finally {
      setSaving(false);
    }
  };

  const probe = async (r: BindingRow) => {
    if (!id || r.routeIds.length === 0) return;
    try {
      const results = await Promise.all(r.routeIds.map((rid) => probeRoute(id, rid)));
      setProbes(results);
    } catch (error) {
      notify.error("adminUpstreams.routes.probeFailed", error);
    }
  };

  const setNow = async (r: BindingRow, on: boolean) => {
    if (!id) return;
    try {
      for (const rid of r.routeIds) await setRouteStatus(id, rid, on ? "enable" : "disable");
      invalidateModels();
      reload();
      onChanged();
    } catch (error) {
      notify.error("adminUpstreams.routes.updateFailed", error);
    }
  };

  const circuit = async (r: BindingRow, action: "open" | "reset") => {
    if (!id) return;
    try {
      for (const rid of r.routeIds) await routeCircuit(id, rid, action);
      reload();
      onChanged();
    } catch (error) {
      notify.error("adminUpstreams.routes.updateFailed", error);
    }
  };

  const confirmDelete = async () => {
    if (!id || !removing) return;
    const ids = removing.flatMap((r) => r.routeIds);
    let result;
    try {
      result = await batchDeleteInChunks(ids, (group) => batchDeleteRoutes(id, group));
    } catch (error) {
      notify.error("adminUpstreams.routes.deleteFailed", error);
      throw error;
    }
    invalidateModels();
    const failed = failedIds(result);
    if (failed.size > 0) {
      toast.error(t("adminCommon.toast.batchDeletePartial", { deleted: result.successCount, missing: result.notFoundCount, failed: result.failedCount }));
      reload();
      onChanged();
      return;
    }
    toast.success(t("adminUpstreams.routes.deletedCount", { count: ids.length }));
    selection.clear();
    reload();
    onChanged();
  };

  // Bulk edits change drafts only; Save writes them.
  const [bulkName, setBulkName] = useState("");
  const [bulkProtocol, setBulkProtocol] = useState("");
  const selected = rows.filter((r) => selection.has(r.key));
  const patchSelected = (fn: (r: BindingRow) => Partial<BindingRow>) => setRows((list) => list.map((r) => (selection.has(r.key) ? { ...r, ...fn(r) } : r)));

  const columns: Column<BindingRow>[] = [
    {
      id: "upstream",
      sticky: "left",
      header: t("adminUpstreams.routes.columns.upstreamModel"),
      cell: (r) => (
        <div className="min-w-0">
          <div className="truncate font-mono text-[12.5px]">{r.upstreamModelName}</div>
          {r.upstreamModelStatus === "inactive" ? <Badge tone="warning">{t("adminUpstreams.routes.delisted")}</Badge> : null}
        </div>
      ),
    },
    {
      id: "platform",
      header: t("adminUpstreams.routes.columns.platformModel"),
      cell: (r) => <Input value={r.platformModelName} onChange={(e) => update(r.key, { platformModelName: e.target.value })} placeholder={t("adminUpstreams.routes.unbound")} aria-label={`${t("adminUpstreams.routes.columns.platformModel")}: ${r.upstreamModelName}`} className="h-8 w-48" />,
    },
    {
      id: "protocols",
      header: t("adminUpstreams.routes.columns.protocols"),
      cell: (r) => (
        <MultiSelectMenu
          values={r.protocols}
          onChange={(values) => {
            const next = resolveSelection(r.protocols, values);
            update(r.key, { protocols: next });
          }}
          options={PROTOCOLS.map((p) => ({ value: p, label: protocolLabel(p), text: protocolLabel(p) }))}
          label={`${t("adminUpstreams.routes.columns.protocols")}: ${r.upstreamModelName}`}
          placeholder={t("adminUpstreams.routes.noProtocol")}
          className="h-8 w-56"
        />
      ),
    },
    {
      id: "kinds",
      header: t("adminUpstreams.routes.columns.kinds"),
      cell: (r) => (
        <div className="flex max-w-40 flex-wrap gap-1">
          {kindsOfRow(r).map((k) => (
            <Badge key={k} tone="outline">
              {t(`adminUpstreams.kinds.${k}`)}
            </Badge>
          ))}
        </div>
      ),
    },
    {
      id: "status",
      header: t("adminUpstreams.routes.columns.status"),
      cell: (r) => (
        <div className="flex items-center gap-2">
          <Switch checked={r.routeStatus === "active"} aria-label={`${r.upstreamModelName}: ${t("adminUpstreams.routes.columns.status")}`} onCheckedChange={(on) => update(r.key, { routeStatus: on ? "active" : "inactive", statusTouched: true })} />
          {r.circuitOpen ? <Badge tone="danger">{t("adminUpstreams.circuitOpen")}</Badge> : null}
        </div>
      ),
    },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (r) => {
        const noRoute = r.routeIds.length === 0;
        const probeBlocked = noRoute || r.upstreamModelStatus === "inactive" || upstream?.status === "inactive";
        return (
          <CellActions>
            <DropdownMenu>
              <DropdownMenuTrigger asChild>
                <Button variant="ghost" size="icon-sm" aria-label={`${t("adminCommon.table.moreActions")}: ${r.upstreamModelName}`}>
                  <MoreHorizontal />
                </Button>
              </DropdownMenuTrigger>
              <DropdownMenuContent>
                <DropdownMenuItem disabled={probeBlocked} onSelect={() => void probe(r)}>
                  <Activity />
                  {t("adminUpstreams.routes.probe")}
                </DropdownMenuItem>
                <DropdownMenuItem disabled={noRoute} onSelect={() => void setNow(r, r.routeStatus !== "active")}>
                  {r.routeStatus === "active" ? <PowerOff /> : <Power />}
                  {r.routeStatus === "active" ? t("adminUpstreams.routes.disableNow") : t("adminUpstreams.routes.enableNow")}
                </DropdownMenuItem>
                <DropdownMenuItem disabled={noRoute} onSelect={() => void circuit(r, r.circuitOpen ? "reset" : "open")}>
                  {r.circuitOpen ? <RotateCcw /> : <Zap />}
                  {r.circuitOpen ? t("adminUpstreams.routes.resetCircuit") : t("adminUpstreams.routes.openCircuit")}
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem destructive disabled={noRoute} onSelect={() => setRemoving([r])}>
                  <Trash2 />
                  {t("common.actions.delete")}
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </CellActions>
        );
      },
    },
  ];

  return (
    <>
      <Dialog open={upstream !== null} onOpenChange={(open) => !open && !saving && onClose()}>
        <DialogContent className="max-w-6xl">
          <DialogHeader>
            <DialogTitle>{t("adminUpstreams.routes.title", { name: upstream?.name ?? "" })}</DialogTitle>
            <DialogDescription>{t("adminUpstreams.routes.description")}</DialogDescription>
          </DialogHeader>
          <Toolbar>
            <SearchBox value={q} onChange={setQ} placeholder={t("adminUpstreams.routes.search")} />
            <OptionSelect
              value={routeStatus}
              onChange={setRouteStatusFilter}
              label={t("adminUpstreams.routes.columns.status")}
              options={[
                { value: "bound", label: t("adminUpstreams.routes.filters.bound") },
                { value: "active", label: t("adminUpstreams.routes.filters.active") },
                { value: "inactive", label: t("adminUpstreams.routes.filters.inactive") },
              ]}
              disabled={dirty}
            />
            <OptionSelect
              value={upstreamStatus}
              onChange={setUpstreamStatus}
              label={t("adminUpstreams.routes.upstreamStatus")}
              options={[
                { value: "", label: t("adminUpstreams.routes.filters.anyUpstreamStatus") },
                { value: "active", label: t("adminUpstreams.routes.filters.listed") },
                { value: "inactive", label: t("adminUpstreams.routes.filters.delisted") },
              ]}
              disabled={dirty}
            />
            <OptionSelect value={protocol} onChange={setProtocol} label={t("adminUpstreams.routes.columns.protocols")} options={[{ value: "", label: t("adminUpstreams.routes.filters.anyProtocol") }, ...PROTOCOLS.map((p) => ({ value: p, label: protocolLabel(p) }))]} disabled={dirty} />
            <OptionSelect value={sort} onChange={setSort} label={t("adminUpstreams.routes.sort")} options={SORTS.map((s) => ({ value: s, label: t(`adminUpstreams.routes.sorts.${s}`) }))} disabled={dirty} />
            <Button size="sm" variant="outline" className="ml-auto" onClick={() => setAdding(true)}>
              <Plus />
              {t("adminUpstreams.routes.newBinding")}
            </Button>
          </Toolbar>
          {dirty ? <p className="text-[13px] text-warning-foreground dark:text-warning">{t("adminUpstreams.routes.unsaved")}</p> : null}
          <BulkBar count={selected.length} onClear={selection.clear}>
            <span className="flex items-center gap-1">
              <Input value={bulkName} onChange={(e) => setBulkName(e.target.value)} placeholder={t("adminUpstreams.routes.columns.platformModel")} aria-label={t("adminUpstreams.routes.columns.platformModel")} className="h-8 w-44" />
              <Button size="sm" variant="outline" onClick={() => patchSelected(() => ({ platformModelName: bulkName }))}>
                {t("adminUpstreams.routes.bulk.name")}
              </Button>
            </span>
            <span className="flex items-center gap-1">
              <OptionSelect value={bulkProtocol} onChange={setBulkProtocol} label={t("adminUpstreams.routes.columns.protocols")} placeholder={t("adminUpstreams.routes.noProtocol")} options={PROTOCOLS.map((p) => ({ value: p, label: protocolLabel(p) }))} className="h-8 w-56" />
              <Button size="sm" variant="outline" disabled={!bulkProtocol} onClick={() => patchSelected(() => ({ protocols: [bulkProtocol] }))}>
                {t("adminUpstreams.routes.bulk.protocol")}
              </Button>
            </span>
            <Button size="sm" variant="outline" onClick={() => patchSelected((r) => (r.upstreamModelStatus === "inactive" ? {} : { routeStatus: "active", statusTouched: true }))}>
              {t("adminUpstreams.routes.bulk.enable")}
            </Button>
            <Button size="sm" variant="outline" onClick={() => patchSelected((r) => (r.upstreamModelStatus === "inactive" ? {} : { routeStatus: "inactive", statusTouched: true }))}>
              {t("adminUpstreams.routes.bulk.disable")}
            </Button>
            <Button size="sm" variant="destructive" onClick={() => setRemoving(selected.filter((r) => r.routeIds.length > 0))}>
              {t("common.actions.delete")}
            </Button>
          </BulkBar>
          <div className="max-h-[50vh] overflow-auto">
            <DataTable columns={columns} rows={rows} rowKey={keyOf} loading={loading} selection={selection} empty={t("adminUpstreams.routes.empty")} rowLabel={(r) => r.upstreamModelName || r.platformModelName} label={t("adminUpstreams.routes.title", { name: upstream?.name ?? "" })} />
          </div>
          <Pagination page={page} pageSize={pageSize} total={total} onPage={setPage} onPageSize={setPageSize} disabled={loading || dirty} />
          <DialogFooter>
            <Button variant="outline" disabled={saving} onClick={onClose}>
              {t("common.actions.close")}
            </Button>
            {dirty ? (
              <Button variant="outline" disabled={saving} onClick={reload}>
                {t("adminCommon.settings.revert")}
              </Button>
            ) : null}
            <Button loading={saving} onClick={save}>
              {t("common.actions.save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>

      <NewBindingDialog
        open={adding}
        upstreamId={id ?? 0}
        onOpenChange={setAdding}
        onCreated={() => {
          invalidateModels();
          reload();
          onChanged();
        }}
      />
      <ProbeDialog
        results={probes}
        onClose={() => setProbes(null)}
        onDeleteRoute={async (result) => {
          if (!id) return;
          try {
            await deleteRoute(id, result.routeID);
          } catch (error) {
            notify.error("adminUpstreams.routes.deleteFailed", error);
            throw error;
          }
          invalidateModels();
          toast.success(t("adminUpstreams.routes.deletedCount", { count: 1 }));
          reload();
          onChanged();
        }}
      />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={t("adminUpstreams.routes.deleteTitle", { count: removing?.reduce((n, r) => n + r.routeIds.length, 0) ?? 0 })}
        description={t("adminUpstreams.routes.deleteBody")}
        confirmLabel={t("common.actions.delete")}
        destructive
        onConfirm={confirmDelete}
      />
    </>
  );
}

function NewBindingDialog({ open, upstreamId, onOpenChange, onCreated }: { open: boolean; upstreamId: number; onOpenChange: (open: boolean) => void; onCreated: () => void }) {
  const t = useT();
  const message = useErrorMessage();
  const [upstreamModel, setUpstreamModel] = useState("");
  const [platformModel, setPlatformModel] = useState("");
  const [protocols, setProtocols] = useState<string[]>(["openai_chat_completions"]);
  const [active, setActive] = useState(true);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (open) {
      setUpstreamModel("");
      setPlatformModel("");
      setProtocols(["openai_chat_completions"]);
      setActive(true);
      setError(null);
    }
  }, [open]);
  const submit = async () => {
    const problem = newBindingProblem(upstreamModel, platformModel);
    if (problem) {
      setError(t(`adminUpstreams.routes.problems.${problem === "upstreamModel" ? "upstreamModel" : "platformModelNew"}`));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const kinds = kindsOfRow({ protocols, kinds: [] });
      await upsertBinding(upstreamId, { upstreamModelName: upstreamModel.trim(), platformModelName: platformModel.trim(), kindsJSON: kindsJson(kinds.length ? kinds : [KINDS[0]]), protocols, status: active ? "active" : "inactive", priority: 1, weight: 1 });
      onCreated();
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("adminUpstreams.routes.newBindingTitle")}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-4">
          <Field required>
            <FieldLabel>{t("adminUpstreams.routes.columns.upstreamModel")}</FieldLabel>
            <Input value={upstreamModel} onChange={(e) => setUpstreamModel(e.target.value)} autoComplete="off" spellCheck={false} />
          </Field>
          <Field required>
            <FieldLabel>{t("adminUpstreams.routes.columns.platformModel")}</FieldLabel>
            <Input value={platformModel} onChange={(e) => setPlatformModel(e.target.value)} autoComplete="off" spellCheck={false} />
          </Field>
          <Field>
            <FieldLabel>{t("adminUpstreams.routes.columns.protocols")}</FieldLabel>
            <MultiSelectMenu values={protocols} onChange={(v) => setProtocols(resolveSelection(protocols, v))} options={PROTOCOLS.map((p) => ({ value: p, label: protocolLabel(p), text: protocolLabel(p) }))} className="w-full" />
          </Field>
          <label className="flex items-center gap-3 text-sm font-medium">
            <Switch checked={active} onCheckedChange={setActive} />
            {t("adminUpstreams.routes.filters.active")}
          </label>
          <FieldError>{error}</FieldError>
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={busy} onClick={submit}>
            {t("common.actions.create")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
