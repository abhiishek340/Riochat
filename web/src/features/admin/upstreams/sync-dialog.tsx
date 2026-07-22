"use client";

import { TriangleAlert } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast-store";
import { ApiError, isAbortError } from "@/lib/api/errors";
import { useCapabilities } from "@/lib/capabilities/provider";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { Checkbox } from "../components/checkbox";
import { MultiSelectMenu } from "../components/menu-select";
import { SearchBox } from "../components/toolbar";
import { protocolLabel } from "../lib/protocols";
import { invalidateModels, loadPermissionGroups } from "../lib/reference";
import { importRemote, previewRemote, syncRemote, type RemotePreview, type Upstream } from "../llm-api";
import type { PermissionGroup } from "../lib/wire";
import { allowEmpty, buildRows, canApply, defaultSelection, importItems, needsConfirmation, OUTCOMES, outcomeCounts, rowsForOutcomes, searchRows, syncSummary, type Outcome, type SyncRow } from "./sync";

export function SyncDialog({ upstream, onClose, onChanged }: { upstream: Upstream | null; onClose: () => void; onChanged: () => void }) {
  const t = useT();
  const message = useErrorMessage();
  const { flags } = useCapabilities();
  const id = upstream?.id;
  const [preview, setPreview] = useState<RemotePreview | null>(null);
  const [loading, setLoading] = useState(false);
  const [applying, setApplying] = useState(false);
  const [rows, setRows] = useState<SyncRow[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [names, setNames] = useState<Record<string, string>>({});
  const [query, setQuery] = useState("");
  const [chips, setChips] = useState<Set<Outcome>>(new Set());
  const [groups, setGroups] = useState<PermissionGroup[]>([]);
  const [groupIds, setGroupIds] = useState<string[]>([]);
  const [confirm, setConfirm] = useState(false);
  const [unresolvedOpen, setUnresolvedOpen] = useState(false);
  const [nonce, setNonce] = useState(0);

  // Loading is abortable; closing the dialog cancels it. A failed load closes the dialog.
  useEffect(() => {
    if (id === undefined) return;
    const controller = new AbortController();
    setLoading(true);
    setPreview(null);
    previewRemote(id, controller.signal).then(
      (result) => {
        const built = buildRows(result.items ?? [], result.syncPlan);
        setPreview(result);
        setRows(built);
        setSelected(defaultSelection(built));
        setNames({});
        setLoading(false);
      },
      (error: unknown) => {
        if (isAbortError(error)) return;
        setLoading(false);
        toast.error(t("adminUpstreams.sync.loadFailed"), { description: message(error) });
        onClose();
      },
    );
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, nonce]);

  useEffect(() => {
    if (id === undefined || !flags.multiUser) return;
    void loadPermissionGroups().then(
      (list) => {
        setGroups(list);
        setGroupIds(list.filter((g) => g.isDefault).map((g) => String(g.id)));
      },
      () => undefined,
    );
  }, [id, flags.multiUser]);

  const plan = preview?.syncPlan ?? null;
  const counts = plan ? outcomeCounts(plan) : null;
  const visible = useMemo(() => (plan ? searchRows(rowsForOutcomes(rows, plan, chips), query) : []), [rows, plan, chips, query]);
  const keys = visible.filter((r) => !r.vanished).map((r) => r.upstreamModelName);
  const ready = plan !== null && canApply({ loading, importing: applying, snapshotId: preview?.snapshotID ?? "", plan, selected: selected.size });

  const apply = async () => {
    if (!id || !preview || !plan) return;
    setApplying(true);
    let synced = null as Awaited<ReturnType<typeof syncRemote>> | null;
    try {
      synced = await syncRemote(id, { allowEmpty: allowEmpty(preview.items ?? []), expectedSnapshot: preview.snapshotID });
      const items = importItems(rows, selected, names, synced.syncedModels ?? []);
      let imported = 0;
      if (items.length > 0) {
        const result = await importRemote(id, { items, permissionGroupIDs: flags.multiUser ? groupIds.map(Number) : undefined });
        imported = result.importedCount;
        if (result.failedCount > 0) toast.warning(t("adminUpstreams.sync.importPartial", { ok: result.importedCount, failed: result.failedCount }), { description: result.results.find((r) => r.error)?.error });
      }
      invalidateModels();
      const s = syncSummary(synced);
      toast.success(t("adminUpstreams.sync.done", { created: s.created, updated: s.updated, imported }));
      onChanged();
      onClose();
    } catch (error) {
      if (synced) {
        // The catalog changed but the import did not: say so and refresh the list behind.
        const s = syncSummary(synced);
        toast.error(t("adminUpstreams.sync.importFailedAfterSync"), { description: `${t("adminUpstreams.sync.summary", { created: s.created, updated: s.updated, inactivated: s.inactivated })} ${message(error)}` });
        invalidateModels();
        onChanged();
      } else if (error instanceof ApiError && error.errorCode === "llm.remote_models_snapshot_changed") {
        toast.warning(t("adminUpstreams.sync.snapshotChanged"));
        setNonce((n) => n + 1);
      } else if (!isAbortError(error)) {
        toast.error(t("adminUpstreams.sync.applyFailed"), { description: message(error) });
      }
    } finally {
      setApplying(false);
    }
  };

  const toggleChip = (o: Outcome) =>
    setChips((s) => {
      const next = new Set(s);
      if (next.has(o)) next.delete(o);
      else next.add(o);
      return next;
    });

  return (
    <>
      <Dialog open={upstream !== null} onOpenChange={(open) => !open && !applying && onClose()}>
        <DialogContent className="max-w-5xl">
          <DialogHeader>
            <DialogTitle>{t("adminUpstreams.sync.title", { name: upstream?.name ?? "" })}</DialogTitle>
            <DialogDescription>{t("adminUpstreams.sync.description")}</DialogDescription>
          </DialogHeader>
          {loading || !plan || !counts ? (
            <div className="space-y-2" role="status" aria-label={t("adminCommon.common.loading")}>
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-48 w-full" />
            </div>
          ) : (
            <>
              <div className="flex flex-wrap gap-1.5" role="group" aria-label={t("adminUpstreams.sync.outcomes.title")}>
                {OUTCOMES.map((o) => (
                  <button
                    key={o}
                    type="button"
                    aria-pressed={chips.has(o)}
                    onClick={() => toggleChip(o)}
                    className={`rounded-full border px-2.5 py-1 text-xs font-medium transition-colors ${chips.has(o) ? "border-primary bg-primary/10 text-primary" : "text-muted-foreground hover:bg-muted"} ${o === "inactivatedModels" && counts[o] > 0 ? "border-destructive/50 text-destructive" : ""}`}
                  >
                    {t(`adminUpstreams.sync.outcomes.${o}`)} · {counts[o]}
                  </button>
                ))}
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <SearchBox value={query} onChange={setQuery} placeholder={t("adminUpstreams.sync.search")} className="sm:w-72" />
                {flags.multiUser && groups.length > 0 ? (
                  <MultiSelectMenu values={groupIds} onChange={setGroupIds} options={groups.map((g) => ({ value: String(g.id), label: g.name, text: g.name }))} placeholder={t("adminUpstreams.sync.groups")} label={t("adminUpstreams.sync.groups")} className="h-9 w-56" />
                ) : null}
                <span className="ml-auto text-sm text-muted-foreground">{t("adminUpstreams.sync.selected", { count: selected.size })}</span>
              </div>
              {counts.unresolvedProtocolModels > 0 ? (
                <div className="rounded-lg border border-warning/50 bg-warning/10 px-3 py-2 text-sm">
                  <button type="button" className="flex w-full items-center gap-2 text-left font-medium" aria-expanded={unresolvedOpen} onClick={() => setUnresolvedOpen((v) => !v)}>
                    <TriangleAlert aria-hidden className="size-4 text-warning" />
                    {t("adminUpstreams.sync.unresolved", { count: counts.unresolvedProtocolModels })}
                  </button>
                  {unresolvedOpen ? <p className="mt-2 break-words font-mono text-xs text-muted-foreground">{plan.unresolvedProtocolModels.join(", ")}</p> : null}
                </div>
              ) : null}
              <div className="max-h-[45vh] overflow-auto rounded-lg border">
                <table className="w-full text-sm">
                  <thead className="sticky top-0 z-10 bg-muted/90 text-left text-xs uppercase tracking-wide text-muted-foreground">
                    <tr>
                      <th className="w-10 px-3 py-2">
                        <Checkbox
                          aria-label={t("adminCommon.table.selectAll")}
                          checked={keys.length > 0 && keys.every((k) => selected.has(k)) ? true : keys.some((k) => selected.has(k)) ? "indeterminate" : false}
                          disabled={keys.length === 0}
                          onCheckedChange={(on) =>
                            setSelected((s) => {
                              const next = new Set(s);
                              for (const k of keys) {
                                if (on) next.add(k);
                                else next.delete(k);
                              }
                              return next;
                            })
                          }
                        />
                      </th>
                      <th className="px-3 py-2">{t("adminUpstreams.sync.columns.upstream")}</th>
                      <th className="px-3 py-2">{t("adminUpstreams.sync.columns.platform")}</th>
                      <th className="px-3 py-2">{t("adminUpstreams.sync.columns.protocols")}</th>
                      <th className="px-3 py-2">{t("adminUpstreams.sync.columns.state")}</th>
                    </tr>
                  </thead>
                  <tbody>
                    {visible.map((r) => (
                      <tr key={r.upstreamModelName} className={`border-t ${r.vanished ? "bg-destructive/5" : ""}`}>
                        <td className="px-3 py-2">
                          <Checkbox
                            aria-label={t("adminCommon.table.selectRowNamed", { name: r.upstreamModelName })}
                            checked={selected.has(r.upstreamModelName)}
                            disabled={r.vanished}
                            onCheckedChange={(on) =>
                              setSelected((s) => {
                                const next = new Set(s);
                                if (on) next.add(r.upstreamModelName);
                                else next.delete(r.upstreamModelName);
                                return next;
                              })
                            }
                          />
                        </td>
                        <td className="max-w-[16rem] px-3 py-2 font-mono text-[12.5px]">
                          <div className="truncate">{r.upstreamModelName}</div>
                        </td>
                        <td className="px-3 py-2">
                          {r.vanished || r.alreadyBound ? (
                            <span className="text-muted-foreground">{r.boundPlatformModels.join(", ") || "-"}</span>
                          ) : (
                            <Input
                              value={names[r.upstreamModelName] ?? r.suggestedPlatformModelName ?? ""}
                              placeholder={r.upstreamModelName}
                              onChange={(e) => setNames((n) => ({ ...n, [r.upstreamModelName]: e.target.value }))}
                              aria-label={`${t("adminUpstreams.sync.columns.platform")}: ${r.upstreamModelName}`}
                              className="h-8 w-52"
                            />
                          )}
                        </td>
                        <td className="px-3 py-2 text-[13px] text-muted-foreground">{r.protocols.map(protocolLabel).join(", ") || t("adminUpstreams.sync.noProtocol")}</td>
                        <td className="px-3 py-2">
                          {r.vanished ? <Badge tone="danger">{t("adminUpstreams.sync.willInactivate")}</Badge> : r.alreadyBound ? <Badge tone="neutral">{t("adminUpstreams.sync.bound")}</Badge> : <Badge tone="primary">{t("adminUpstreams.sync.new")}</Badge>}
                        </td>
                      </tr>
                    ))}
                    {visible.length === 0 ? (
                      <tr>
                        <td colSpan={5} className="h-24 text-center text-muted-foreground">
                          {t("adminUpstreams.sync.empty")}
                        </td>
                      </tr>
                    ) : null}
                  </tbody>
                </table>
              </div>
            </>
          )}
          <DialogFooter>
            <Button variant="outline" disabled={applying} onClick={onClose}>
              {t("common.actions.cancel")}
            </Button>
            <Button loading={applying} disabled={!ready} onClick={() => (plan && needsConfirmation(plan) ? setConfirm(true) : void apply())}>
              {plan && !loading && !canApply({ loading, importing: applying, snapshotId: preview?.snapshotID ?? "", plan, selected: selected.size }) && !applying ? t("adminUpstreams.sync.upToDate") : t("adminUpstreams.sync.apply")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={confirm}
        onOpenChange={setConfirm}
        title={t("adminUpstreams.sync.confirmTitle", { count: plan?.inactivatedModels.length ?? 0 })}
        description={t("adminUpstreams.sync.confirmBody")}
        confirmLabel={t("adminUpstreams.sync.apply")}
        destructive
        onConfirm={() => void apply()}
      />
    </>
  );
}
