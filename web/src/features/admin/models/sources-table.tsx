"use client";

import { Activity, Gauge, Trash2, Zap, RotateCcw } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input, NativeSelect } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { useI18n, useT } from "@/lib/i18n/provider";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { Pagination } from "../components/pagination";
import { formatDateTime } from "../lib/format";
import { parseKinds, protocolLabel } from "../lib/protocols";
import type { PlatformModel } from "../lib/wire";
import type { ProbeResult, SourceView } from "../llm-api";
import { nonNegative, protocolChoices } from "./rules";
import type { SourcesController } from "./use-sources";

/** A whole number edited in place: Enter or leaving the field commits, anything invalid reverts. */
export function IntCell({ value, label, disabled, onCommit }: { value: number; label: string; disabled?: boolean; onCommit: (text: string) => boolean }) {
  const [text, setText] = useState(String(value));
  // Escape blurs the field; that blur must not commit the text Escape threw away.
  const reverted = useRef(false);
  useEffect(() => setText(String(value)), [value]);
  const commit = () => {
    if (reverted.current) {
      reverted.current = false;
      return;
    }
    if (text.trim() === String(value)) return;
    if (!onCommit(text)) setText(String(value));
  };
  return (
    <Input
      inputMode="numeric"
      value={text}
      disabled={disabled}
      aria-label={label}
      onChange={(e) => setText(e.target.value)}
      onBlur={commit}
      onKeyDown={(e) => {
        if (e.key === "Enter") e.currentTarget.blur();
        if (e.key === "Escape") {
          reverted.current = true;
          setText(String(value));
          e.currentTarget.blur();
        }
      }}
      className="h-8 w-16 tabular-nums"
    />
  );
}

export function SourcesTable({ model, ctl, breakerOn, onProbe, compact = false }: { model: PlatformModel; ctl: SourcesController; breakerOn: boolean; onProbe: (results: ProbeResult[]) => void; compact?: boolean }) {
  const t = useT();
  const { locale } = useI18n();
  const [removing, setRemoving] = useState<SourceView | null>(null);
  const [tuning, setTuning] = useState<SourceView | null>(null);
  const [opening, setOpening] = useState<SourceView | null>(null);
  const kinds = parseKinds(model.kindsJSON);
  const { list } = ctl;

  const columns: Column<SourceView>[] = [
    {
      id: "upstream",
      header: t("adminModels.sources.columns.upstream"),
      cell: (s) => (
        <div className="min-w-0">
          <div className="flex items-center gap-1.5">
            <span className="truncate font-medium">{s.upstreamName}</span>
            {s.upstreamStatus !== "active" ? <Badge tone="neutral">{t("adminModels.sources.upstreamOff")}</Badge> : null}
          </div>
          <div className="flex items-center gap-1.5">
            <span className="truncate font-mono text-[12px] text-muted-foreground">{s.upstreamModelName}</span>
            {s.upstreamModelStatus !== "active" ? <Badge tone="warning">{t("adminModels.sources.delisted")}</Badge> : null}
          </div>
        </div>
      ),
    },
    {
      id: "protocol",
      header: t("adminModels.sources.columns.protocol"),
      cell: (s) => (
        <NativeSelect value={s.protocol} disabled={ctl.isPending(s.id)} aria-label={`${t("adminModels.sources.columns.protocol")}: ${s.upstreamModelName}`} onChange={(e) => ctl.setProtocol(s, e.target.value)} className="h-8 w-52">
          {s.protocol === "" ? <option value="">-</option> : null}
          {protocolChoices(kinds, s.protocol).map((p) => (
            <option key={p} value={p}>
              {protocolLabel(p)}
            </option>
          ))}
        </NativeSelect>
      ),
    },
    { id: "priority", header: t("adminModels.sources.columns.priority"), cell: (s) => <IntCell value={s.priority} label={`${t("adminModels.sources.columns.priority")}: ${s.upstreamModelName}`} disabled={ctl.isPending(s.id)} onCommit={(v) => ctl.setNumber(s, "priority", v)} /> },
    { id: "weight", header: t("adminModels.sources.columns.weight"), cell: (s) => <IntCell value={s.weight} label={`${t("adminModels.sources.columns.weight")}: ${s.upstreamModelName}`} disabled={ctl.isPending(s.id)} onCommit={(v) => ctl.setNumber(s, "weight", v)} /> },
    {
      id: "status",
      header: t("adminModels.sources.columns.status"),
      cell: (s) => <Switch checked={s.status === "active"} disabled={ctl.isPending(s.id)} aria-label={`${s.upstreamModelName}: ${t("adminModels.sources.columns.status")}`} onCheckedChange={(on) => void ctl.toggle(s, on)} />,
    },
  ];
  if (breakerOn) {
    columns.push({
      id: "circuit",
      header: t("adminModels.sources.columns.circuit"),
      cell: (s) =>
        s.circuitOpen ? (
          <div className="space-y-0.5">
            <Badge tone="danger">{t(s.circuitScope === "upstream" ? "adminModels.sources.circuitUpstream" : "adminModels.sources.circuitSource")}</Badge>
            {s.circuitUntil ? <div className="text-xs text-muted-foreground">{t("adminModels.sources.until", { time: formatDateTime(s.circuitUntil, locale) })}</div> : null}
          </div>
        ) : (
          <span className="text-muted-foreground">-</span>
        ),
    });
  }
  columns.push({
    id: "actions",
    sticky: "right",
    header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
    cell: (s) => (
      <CellActions>
        <Button variant="ghost" size="icon-sm" disabled={ctl.probing === s.id} aria-label={`${t("adminModels.sources.probe")}: ${s.upstreamModelName}`} title={t("adminModels.sources.probe")} onClick={async () => {
          const out = await ctl.probe(s);
          if (out) onProbe(out);
        }}>
          <Activity />
        </Button>
        {breakerOn ? (
          s.circuitOpen ? (
            <Button variant="ghost" size="icon-sm" aria-label={`${t("adminModels.sources.resetCircuit")}: ${s.upstreamModelName}`} title={t("adminModels.sources.resetCircuit")} onClick={() => void ctl.resetCircuit(s)}>
              <RotateCcw />
            </Button>
          ) : (
            <Button variant="ghost" size="icon-sm" aria-label={`${t("adminModels.sources.openCircuit")}: ${s.upstreamModelName}`} title={t("adminModels.sources.openCircuit")} onClick={() => setOpening(s)}>
              <Zap />
            </Button>
          )
        ) : null}
        <Button variant="ghost" size="icon-sm" aria-label={`${t("adminModels.sources.circuitSettings")}: ${s.upstreamModelName}`} title={t("adminModels.sources.circuitSettings")} onClick={() => setTuning(s)}>
          <Gauge />
        </Button>
        <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.delete")}: ${s.upstreamModelName}`} title={t("common.actions.delete")} onClick={() => setRemoving(s)}>
          <Trash2 className="text-destructive" />
        </Button>
      </CellActions>
    ),
  });

  return (
    <div className="space-y-3">
      <DataTable columns={columns} rows={list.rows} rowKey={(s) => s.id} loading={list.loading} error={list.error} onRetry={list.reload} fetching={list.fetching} empty={t("adminModels.sources.empty")} label={t("adminModels.sources.title", { name: model.platformModelName })} skeletonRows={compact ? 2 : 4} />
      {list.total > list.pageSize ? <Pagination page={list.page} pageSize={list.pageSize} total={list.total} onPage={list.setPage} disabled={list.fetching} /> : null}
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={t("adminModels.sources.deleteTitle")}
        description={removing ? t("adminModels.sources.deleteBody", { upstream: removing.upstreamName, model: removing.upstreamModelName }) : undefined}
        confirmLabel={t("common.actions.delete")}
        destructive
        onConfirm={async () => {
          if (removing) await ctl.remove(removing);
        }}
      />
      <ConfirmDialog
        open={opening !== null}
        onOpenChange={(open) => !open && setOpening(null)}
        title={t("adminModels.sources.openTitle")}
        description={t("adminModels.sources.openBody")}
        confirmLabel={t("adminModels.sources.openCircuit")}
        destructive
        onConfirm={async () => {
          if (opening) await ctl.openCircuit(opening);
        }}
      />
      <CircuitDialog source={tuning} enforced={model.cbPolicyMode === "enforced"} onClose={() => setTuning(null)} onSave={(values) => (tuning ? ctl.setBreaker(tuning, values) : Promise.resolve(false))} />
    </div>
  );
}

function CircuitDialog({ source, enforced, onClose, onSave }: { source: SourceView | null; enforced: boolean; onClose: () => void; onSave: (values: { cbFailureThreshold: number; cbDurationMin: number; cbWindowMin: number }) => Promise<boolean> }) {
  const t = useT();
  const [fail, setFail] = useState("0");
  const [duration, setDuration] = useState("0");
  const [window, setWindow] = useState("0");
  const [busy, setBusy] = useState(false);
  useEffect(() => {
    if (!source) return;
    setFail(String(source.cbFailureThreshold));
    setDuration(String(source.cbDurationMin));
    setWindow(String(source.cbWindowMin));
  }, [source]);
  const field = (label: string, value: string, set: (v: string) => void) => (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      <Input inputMode="numeric" value={value} onChange={(e) => set(e.target.value)} className="tabular-nums" />
    </Field>
  );
  return (
    <Dialog open={source !== null} onOpenChange={(open) => !open && !busy && onClose()}>
      <DialogContent className="max-w-md">
        <DialogHeader>
          <DialogTitle>{t("adminModels.sources.circuitSettings")}</DialogTitle>
          <DialogDescription>{source ? `${source.upstreamName} / ${source.upstreamModelName}` : ""}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-3">
          {field(t("adminModels.breaker.failure"), fail, setFail)}
          {field(t("adminModels.breaker.duration"), duration, setDuration)}
          {field(t("adminModels.breaker.window"), window, setWindow)}
          <p className="text-[13px] text-muted-foreground">{t("adminModels.sources.circuitHint")}</p>
          {enforced ? <p className="text-[13px] text-warning">{t("adminModels.sources.enforcedHint")}</p> : null}
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={onClose}>
            {t("common.actions.cancel")}
          </Button>
          <Button
            loading={busy}
            onClick={async () => {
              setBusy(true);
              const ok = await onSave({ cbFailureThreshold: nonNegative(fail), cbDurationMin: nonNegative(duration), cbWindowMin: nonNegative(window) });
              setBusy(false);
              if (ok) {
                toast.success(t("adminModels.sources.circuitSaved"));
                onClose();
              }
            }}
          >
            {t("common.actions.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
