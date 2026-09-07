"use client";

import { Copy, Download, Eye, FileClock, Pencil, Plus, Trash2 } from "lucide-react";
import { useCallback, useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { Tooltip } from "@/components/ui/tooltip";
import { isAbortError } from "@/lib/api/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { BulkBar } from "../components/bulk-bar";
import { copyText } from "../components/copy-button";
import { LoadError } from "../components/load-error";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { Section } from "../components/page";
import { Pagination } from "../components/pagination";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { runBulk } from "../lib/bulk";
import { datedFileName, downloadJson } from "../lib/download";
import { formatCredit, formatDateTime, formatNumber, formatUsdTable } from "../lib/format";
import { problemText } from "../lib/problem";
import { useNotify } from "../lib/use-notify";
import { usePagedQuery } from "../lib/use-paged-query";
import { useRowUpdates } from "../lib/use-row-updates";
import { useSelection } from "../lib/use-selection";
import { useSnapshot } from "../lib/use-snapshot";
import type { BillingConfig, BillingPlan } from "../lib/wire";
import { batchDeleteCodes, createCodes, listCodeRedemptions, listCodes, patchCode, revealCode, type CodeRedemption } from "./api";
import {
  blankCodeForm,
  createBody,
  editBody,
  editFormOf,
  exportRecord,
  unavailableReason,
  validateCreate,
  validateEditChange,
  type CodeForm,
  type EditForm,
  type RedemptionCode,
} from "./redemption";

export function CodesSection({ config, plans }: { config: BillingConfig; plans: BillingPlan[] }) {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const message = useErrorMessage();
  const [q, setQ] = useState("");
  const [mode, setMode] = useState("");
  const [status, setStatus] = useState("");
  const [onlyAvailable, setOnlyAvailable] = useState(false);
  const [creating, setCreating] = useState(false);
  const [editing, setEditing] = useState<RedemptionCode | null>(null);
  const [records, setRecords] = useState<RedemptionCode | null>(null);
  const [created, setCreated] = useState<RedemptionCode[] | null>(null);
  const [removing, setRemoving] = useState<RedemptionCode[] | null>(null);
  const [busy, setBusy] = useState(false);
  const selection = useSelection<number>();

  // The availability filter depends on the billing mode, so switching it reloads the list.
  const filters = useMemo(() => ({ q, mode, status, availability: onlyAvailable ? "available" : "", billing: config.mode }), [q, mode, status, onlyAvailable, config.mode]);
  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) => listCodes({ page, pageSize, q: f.q, mode: f.mode, status: f.status, availability: f.availability }, signal),
    onError: (error) => notify.error("adminBilling.codes.loadFailed", error),
  });
  useEffect(() => {
    selection.prune(list.rows.map((c) => c.id));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [list.rows]);

  const getKey = useCallback((c: RedemptionCode) => c.id, []);
  const rows = useRowUpdates<RedemptionCode, number>({ getKey, setRows: list.setRows });
  const planName = (id: number) => plans.find((p) => p.id === id)?.name ?? `#${id}`;

  const reward = (c: RedemptionCode) => (c.mode === "period" ? `${planName(c.planID)} · ${t("adminBilling.codes.days", { count: c.durationDays })}` : formatCredit(c.creditUSD, locale));

  const columns: Column<RedemptionCode>[] = [
    {
      id: "code",
      sticky: "left",
      header: t("adminBilling.codes.columns.code"),
      cell: (c) => {
        const reason = unavailableReason(c, config.mode);
        return (
          <div className="min-w-0">
            <div className="flex items-center gap-2">
              <span className="font-mono text-[12.5px]">{c.codeHint}</span>
              {reason ? (
                <Tooltip label={t(`adminBilling.codes.unavailable.${reason}`)}>
                  <Badge tone="outline">{t("adminBilling.codes.unavailableBadge")}</Badge>
                </Tooltip>
              ) : null}
            </div>
            {c.description ? <div className="truncate text-xs text-muted-foreground">{c.description}</div> : null}
          </div>
        );
      },
    },
    { id: "mode", header: t("adminBilling.codes.columns.mode"), cell: (c) => t(`adminBilling.codes.modes.${c.mode === "period" ? "period" : "usage"}`) },
    { id: "reward", header: t("adminBilling.codes.columns.reward"), cell: (c) => reward(c) },
    {
      id: "used",
      header: t("adminBilling.codes.columns.used"),
      className: "tabular-nums",
      cell: (c) => `${formatNumber(c.redeemedCount, locale)} / ${c.maxRedemptions === null ? "∞" : formatNumber(c.maxRedemptions, locale)}`,
    },
    { id: "expires", header: t("adminBilling.codes.columns.expires"), className: "whitespace-nowrap text-muted-foreground", cell: (c) => (c.expiresAt ? formatDateTime(c.expiresAt, locale) : t("adminBilling.codes.never")) },
    {
      id: "status",
      header: t("adminBilling.codes.columns.status"),
      cell: (c) => (
        <Switch
          checked={c.status === "active"}
          disabled={rows.isPending(c.id)}
          aria-label={`${t("adminBilling.codes.columns.status")}: ${c.codeHint}`}
          onCheckedChange={(on) => void rows.update(c, (r) => ({ ...r, status: on ? "active" : "inactive" }), () => patchCode(c.id, { status: on ? "active" : "inactive" }), { failKey: "adminBilling.codes.updateFailed" })}
        />
      ),
    },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (c) => (
        <CellActions>
          <Tooltip label={t("adminBilling.codes.reveal")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("adminBilling.codes.reveal")}: ${c.codeHint}`} onClick={() => void reveal(c)}>
              <Eye />
            </Button>
          </Tooltip>
          <Tooltip label={t("adminBilling.codes.records")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("adminBilling.codes.records")}: ${c.codeHint}`} onClick={() => setRecords(c)}>
              <FileClock />
            </Button>
          </Tooltip>
          <Tooltip label={t("common.actions.edit")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.edit")}: ${c.codeHint}`} onClick={() => setEditing(c)}>
              <Pencil />
            </Button>
          </Tooltip>
          <Tooltip label={t("common.actions.delete")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.delete")}: ${c.codeHint}`} onClick={() => setRemoving([c])}>
              <Trash2 className="text-destructive" />
            </Button>
          </Tooltip>
        </CellActions>
      ),
    },
  ];

  const reveal = async (c: RedemptionCode) => {
    try {
      const plain = await revealCode(c.id);
      if (!plain) throw new Error(t("adminBilling.codes.cannotReveal"));
      setCreated([{ ...c, code: plain }]);
    } catch (error) {
      notify.error("adminBilling.codes.revealFailed", error);
    }
  };

  const selected = list.rows.filter((c) => selection.has(c.id));

  /** Plain codes come from an explicit reveal; codes that cannot be revealed are skipped and counted. */
  const revealAll = async (items: RedemptionCode[]) => {
    const found: Array<{ code: RedemptionCode; plain: string }> = [];
    let skipped = 0;
    for (const c of items) {
      try {
        const plain = await revealCode(c.id);
        if (plain) found.push({ code: c, plain });
        else skipped++;
      } catch {
        skipped++;
      }
    }
    return { found, skipped };
  };

  const copySelected = async () => {
    setBusy(true);
    try {
      const { found, skipped } = await revealAll(selected);
      if (found.length === 0) return notify.warning("adminBilling.codes.nothingToCopy");
      if (await copyText(found.map((f) => f.plain).join("\n"))) toast.success(t("adminBilling.codes.copiedMany", { count: found.length }), skipped ? { description: t("adminBilling.codes.skipped", { count: skipped }) } : undefined);
      else toast.error(t("adminCommon.toast.copyFailed"));
    } finally {
      setBusy(false);
    }
  };

  const exportSelected = async () => {
    setBusy(true);
    try {
      const { found, skipped } = await revealAll(selected);
      if (found.length === 0) return notify.warning("adminBilling.codes.nothingToCopy");
      downloadJson(datedFileName("riochat-redemption-codes", "json"), { exportedAt: new Date().toISOString(), total: found.length, codes: found.map((f) => exportRecord(f.code, f.plain)) });
      if (skipped) toast.info(t("adminBilling.codes.skipped", { count: skipped }));
    } finally {
      setBusy(false);
    }
  };

  const bulkStatus = async (next: "active" | "inactive") => {
    const targets = selected.filter((c) => c.status !== next);
    if (targets.length === 0) return notify.info("adminCommon.toast.alreadyApplied");
    setBusy(true);
    const before = new Map(targets.map((c) => [c.id, c]));
    list.setRows((r) => r.map((c) => (before.has(c.id) ? { ...c, status: next } : c)));
    const outcome = await runBulk(targets, (c) => patchCode(c.id, { status: next }), { chunkSize: 10, sequential: false });
    if (outcome.failed.length > 0) {
      const failed = new Set(outcome.failed.map((f) => f.item.id));
      list.setRows((r) => r.map((c) => (failed.has(c.id) ? (before.get(c.id) ?? c) : c)));
      selection.set(failed);
      toast.error(t("adminCommon.toast.bulkPartial"), { description: `${t("adminCommon.toast.bulkPartialBody", { succeeded: outcome.succeeded.length, failed: failed.size })} ${message(outcome.failed[0]?.error)}` });
    } else {
      notify.success("adminCommon.toast.bulkDone", { count: targets.length });
      selection.clear();
    }
    setBusy(false);
  };

  const confirmDelete = async () => {
    if (!removing) return;
    const ids = removing.map((c) => c.id);
    const before = list.rows;
    list.setRows((r) => r.filter((c) => !ids.includes(c.id)));
    list.setTotal((n) => n - ids.length);
    try {
      const result = { total: 0, successCount: 0, notFoundCount: 0, failedCount: 0 };
      const gone = new Set<number>();
      for (let i = 0; i < ids.length; i += 100) {
        const part = await batchDeleteCodes(ids.slice(i, i + 100));
        result.total += part.total;
        result.successCount += part.successCount;
        result.notFoundCount += part.notFoundCount;
        result.failedCount += part.failedCount;
        for (const row of part.results) if (row.status === "deleted" || row.status === "not_found") gone.add(row.id);
      }
      if (result.failedCount > 0) {
        const kept = before.filter((c) => !gone.has(c.id) && ids.includes(c.id));
        list.setRows((r) => [...r, ...kept]);
        list.setTotal((n) => n + kept.length);
        toast.error(t("adminBilling.codes.deletePartial", { deleted: result.successCount, missing: result.notFoundCount, failed: result.failedCount }));
      } else {
        toast.success(t("adminBilling.codes.deleted", { deleted: result.successCount, missing: result.notFoundCount }));
      }
      selection.clear();
      if (list.rows.length - gone.size <= 0 && list.page > 1) list.setPage(list.page - 1);
      else list.reload();
    } catch (error) {
      list.setRows(() => before);
      list.setTotal((n) => n + ids.length);
      notify.error("adminBilling.codes.deleteFailed", error);
      throw error;
    }
  };

  return (
    <Section
      title={t("adminBilling.codes.title")}
      description={t("adminBilling.codes.description")}
      actions={
        <Button size="sm" onClick={() => setCreating(true)}>
          <Plus />
          {t("adminBilling.codes.create")}
        </Button>
      }
      flush
    >
      <div className="space-y-3 p-4">
        <Toolbar>
          <SearchBox value={q} onChange={setQ} placeholder={t("adminBilling.codes.search")} />
          <OptionSelect value={mode} onChange={setMode} label={t("adminBilling.codes.columns.mode")} options={[{ value: "", label: t("adminBilling.codes.anyMode") }, { value: "usage", label: t("adminBilling.codes.modes.usage") }, { value: "period", label: t("adminBilling.codes.modes.period") }]} />
          <OptionSelect value={status} onChange={setStatus} label={t("adminBilling.codes.columns.status")} options={[{ value: "", label: t("adminBilling.codes.anyStatus") }, { value: "active", label: t("adminBilling.codes.statuses.active") }, { value: "inactive", label: t("adminBilling.codes.statuses.inactive") }]} />
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={onlyAvailable} onCheckedChange={setOnlyAvailable} aria-label={t("adminBilling.codes.onlyAvailable")} />
            {t("adminBilling.codes.onlyAvailable")}
          </label>
        </Toolbar>
        <BulkBar count={selection.count} onClear={selection.clear}>
          <Button size="sm" variant="outline" disabled={busy} onClick={copySelected}>
            <Copy />
            {t("adminBilling.codes.copySelected")}
          </Button>
          <Button size="sm" variant="outline" disabled={busy} onClick={exportSelected}>
            <Download />
            {t("adminBilling.codes.exportSelected")}
          </Button>
          <Button size="sm" variant="outline" disabled={busy} onClick={() => void bulkStatus("active")}>
            {t("adminBilling.codes.enableSelected")}
          </Button>
          <Button size="sm" variant="outline" disabled={busy} onClick={() => void bulkStatus("inactive")}>
            {t("adminBilling.codes.disableSelected")}
          </Button>
          <Button size="sm" variant="destructive" disabled={busy} onClick={() => setRemoving(selected)}>
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
          rowTone={(c) => (unavailableReason(c, config.mode) ? "muted" : undefined)}
          empty={t("adminBilling.codes.empty")}
          rowLabel={(c) => c.codeHint} label={t("adminBilling.codes.title")}
        />
        <Pagination page={list.page} pageSize={list.pageSize} total={list.total} onPage={list.setPage} onPageSize={list.setPageSize} disabled={list.fetching} />
      </div>

      <CreateCodesDialog
        open={creating}
        plans={plans}
        defaultMode={config.mode === "period" ? "period" : "usage"}
        onOpenChange={setCreating}
        onCreated={(items) => {
          setCreated(items);
          list.setRows((r) => [...items.map((c) => ({ ...c, code: undefined })), ...r].slice(0, list.pageSize));
          list.setTotal((n) => n + items.length);
          list.reload();
        }}
      />
      <EditCodeDialog code={editing} onOpenChange={(open) => !open && setEditing(null)} onSaved={(saved) => list.setRows((r) => r.map((c) => (c.id === saved.id ? saved : c)))} />
      <RecordsDialog code={records} onClose={() => setRecords(null)} />
      <ShownCodesDialog codes={created} onClose={() => setCreated(null)} />
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(open) => !open && setRemoving(null)}
        title={t("adminBilling.codes.deleteTitle", { count: removing?.length ?? 0 })}
        description={t("adminBilling.codes.deleteBody")}
        confirmLabel={t("common.actions.delete")}
        destructive
        onConfirm={confirmDelete}
      />
    </Section>
  );
}

function CreateCodesDialog({ open, plans, defaultMode, onOpenChange, onCreated }: { open: boolean; plans: BillingPlan[]; defaultMode: "usage" | "period"; onOpenChange: (open: boolean) => void; onCreated: (codes: RedemptionCode[]) => void }) {
  const t = useT();
  const message = useErrorMessage();
  const [form, setForm] = useState<CodeForm>(blankCodeForm(defaultMode));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (open) {
      setForm({ ...blankCodeForm(defaultMode), planId: plans[0] ? String(plans[0].id) : "" });
      setError(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);
  const set = <K extends keyof CodeForm>(key: K, value: CodeForm[K]) => setForm((f) => ({ ...f, [key]: value }));
  const submit = async () => {
    if (busy) return;
    const found = validateCreate(form);
    if (found) {
      setError(problemText(t, found));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      const items = await createCodes(createBody(form));
      onCreated(items);
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent className="max-w-xl">
        <DialogHeader>
          <DialogTitle>{t("adminBilling.codes.createTitle")}</DialogTitle>
          <DialogDescription>{t("adminBilling.codes.createBody")}</DialogDescription>
        </DialogHeader>
        <form
          className="grid gap-4"
          onSubmit={(event) => {
            event.preventDefault();
            void submit();
          }}
        >
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel>{t("adminBilling.codes.form.mode")}</FieldLabel>
              <OptionSelect value={form.mode} onChange={(v) => set("mode", v === "period" ? "period" : "usage")} options={[{ value: "usage", label: t("adminBilling.codes.modes.usage") }, { value: "period", label: t("adminBilling.codes.modes.period") }]} className="w-full" />
            </Field>
            <Field>
              <FieldLabel>{t("adminBilling.codes.form.quantity")}</FieldLabel>
              <Input inputMode="numeric" value={form.quantity} onChange={(e) => set("quantity", e.target.value)} />
            </Field>
          </div>
          <Field>
            <FieldLabel>{t("adminBilling.codes.form.custom")}</FieldLabel>
            <Input value={form.code} onChange={(e) => set("code", e.target.value)} autoComplete="off" spellCheck={false} />
            <FieldHint>{t("adminBilling.codes.form.customHint")}</FieldHint>
          </Field>
          {form.mode === "usage" ? (
            <Field required>
              <FieldLabel>{t("adminBilling.codes.form.credit")}</FieldLabel>
              <Input inputMode="decimal" value={form.creditUSD} onChange={(e) => set("creditUSD", e.target.value)} />
            </Field>
          ) : (
            <div className="grid gap-4 sm:grid-cols-2">
              <Field required>
                <FieldLabel>{t("adminBilling.codes.form.plan")}</FieldLabel>
                <OptionSelect value={form.planId} onChange={(v) => set("planId", v)} placeholder={t("adminBilling.codes.form.choosePlan")} options={plans.map((p) => ({ value: String(p.id), label: p.name }))} className="w-full" />
              </Field>
              <Field required>
                <FieldLabel>{t("adminBilling.codes.form.days")}</FieldLabel>
                <Input inputMode="numeric" value={form.durationDays} onChange={(e) => set("durationDays", e.target.value)} />
              </Field>
            </div>
          )}
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel>{t("adminBilling.codes.form.max")}</FieldLabel>
              <Input inputMode="numeric" value={form.maxRedemptions} onChange={(e) => set("maxRedemptions", e.target.value)} />
              <FieldHint>{t("adminBilling.codes.form.maxHint")}</FieldHint>
            </Field>
            <Field>
              <FieldLabel>{t("adminBilling.codes.form.perUser")}</FieldLabel>
              <Input inputMode="numeric" value={form.perUserLimit} onChange={(e) => set("perUserLimit", e.target.value)} />
            </Field>
          </div>
          <Field>
            <FieldLabel>{t("adminBilling.codes.form.expires")}</FieldLabel>
            <Input type="datetime-local" value={form.expiresAt} onChange={(e) => set("expiresAt", e.target.value)} />
            <FieldHint>{t("adminBilling.codes.form.expiresHint")}</FieldHint>
          </Field>
          <Field>
            <FieldLabel>{t("adminBilling.codes.form.note")}</FieldLabel>
            <Textarea rows={2} value={form.description} maxLength={255} onChange={(e) => set("description", e.target.value)} />
          </Field>
          <FieldError>{error}</FieldError>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={busy}>
              {t("common.actions.create")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

function EditCodeDialog({ code, onOpenChange, onSaved }: { code: RedemptionCode | null; onOpenChange: (open: boolean) => void; onSaved: (code: RedemptionCode) => void }) {
  const t = useT();
  const message = useErrorMessage();
  const shown = useSnapshot(code);
  const [form, setForm] = useState<EditForm | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  useEffect(() => {
    if (code) {
      setForm(editFormOf(code));
      setError(null);
    }
  }, [code]);
  if (!shown || !form) return <Dialog open={false} onOpenChange={() => undefined} />;
  const set = <K extends keyof EditForm>(key: K, value: EditForm[K]) => setForm((f) => (f ? { ...f, [key]: value } : f));
  const submit = async () => {
    const found = validateEditChange(shown, form);
    if (found) {
      setError(problemText(t, found));
      return;
    }
    const body = editBody(shown, form);
    if (Object.keys(body).length === 0) {
      onOpenChange(false);
      return;
    }
    setBusy(true);
    setError(null);
    try {
      onSaved(await patchCode(shown.id, body));
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };
  return (
    <Dialog open={code !== null} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("adminBilling.codes.editTitle", { code: shown.codeHint })}</DialogTitle>
        </DialogHeader>
        <div className="grid gap-4">
          <label className="flex items-center gap-3 text-sm font-medium">
            <Switch checked={form.status === "active"} onCheckedChange={(on) => set("status", on ? "active" : "inactive")} />
            {t("adminBilling.codes.statuses.active")}
          </label>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel>{t("adminBilling.codes.form.max")}</FieldLabel>
              <Input inputMode="numeric" value={form.maxRedemptions} onChange={(e) => set("maxRedemptions", e.target.value)} />
              <FieldHint>{t("adminBilling.codes.form.maxHint")}</FieldHint>
            </Field>
            <Field>
              <FieldLabel>{t("adminBilling.codes.form.perUser")}</FieldLabel>
              <Input inputMode="numeric" value={form.perUserLimit} onChange={(e) => set("perUserLimit", e.target.value)} />
            </Field>
          </div>
          <Field>
            <FieldLabel>{t("adminBilling.codes.form.expires")}</FieldLabel>
            <Input type="datetime-local" value={form.expiresAt} onChange={(e) => set("expiresAt", e.target.value)} />
            <FieldHint>{t("adminBilling.codes.form.expiresHint")}</FieldHint>
          </Field>
          <Field>
            <FieldLabel>{t("adminBilling.codes.form.note")}</FieldLabel>
            <Textarea rows={2} value={form.description} maxLength={255} onChange={(e) => set("description", e.target.value)} />
          </Field>
          <FieldError>{error}</FieldError>
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={busy} onClick={submit}>
            {t("common.actions.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

/** Plain codes are shown once, with copy buttons. */
function ShownCodesDialog({ codes, onClose }: { codes: RedemptionCode[] | null; onClose: () => void }) {
  const t = useT();
  const shown = useSnapshot(codes);
  const list = shown ?? [];
  const all = list.map((c) => c.code ?? "").filter(Boolean).join("\n");
  const copy = async (text: string) => {
    if (await copyText(text)) toast.success(t("adminCommon.toast.copied"));
    else toast.error(t("adminCommon.toast.copyFailed"));
  };
  return (
    <Dialog open={codes !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("adminBilling.codes.shownTitle")}</DialogTitle>
          <DialogDescription>{t("adminBilling.codes.shownBody")}</DialogDescription>
        </DialogHeader>
        <ul className="max-h-72 space-y-1 overflow-auto rounded-lg border p-2">
          {list.map((c) => (
            <li key={c.id} className="flex items-center justify-between gap-2 rounded px-2 py-1 hover:bg-muted/50">
              <span className="break-all font-mono text-[13px]">{c.code}</span>
              <Button variant="ghost" size="icon-sm" aria-label={t("common.actions.copy")} onClick={() => void copy(c.code ?? "")}>
                <Copy />
              </Button>
            </li>
          ))}
        </ul>
        <DialogFooter>
          {list.length > 1 ? (
            <Button variant="outline" onClick={() => void copy(all)}>
              <Copy />
              {t("adminBilling.codes.copyAll")}
            </Button>
          ) : null}
          <Button onClick={onClose}>{t("common.actions.close")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

function RecordsDialog({ code, onClose }: { code: RedemptionCode | null; onClose: () => void }) {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const shown = useSnapshot(code);
  const [page, setPage] = useState(1);
  const [rows, setRows] = useState<CodeRedemption[]>([]);
  const [total, setTotal] = useState(0);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  useEffect(() => {
    if (!code) return;
    // Another code starts clean: no rows of the previous one while this one loads.
    setPage(1);
    setRows([]);
    setTotal(0);
  }, [code]);
  useEffect(() => {
    if (!code) return;
    const controller = new AbortController();
    setLoading(true);
    setFailed(false);
    listCodeRedemptions(code.id, page, 25, controller.signal).then(
      (r) => {
        setRows(r.results);
        setTotal(r.total);
        setLoading(false);
      },
      (error: unknown) => {
        if (isAbortError(error)) return;
        setLoading(false);
        setFailed(true);
        notify.error("adminBilling.codes.recordsFailed", error);
      },
    );
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [code, page, attempt]);
  const money = (nano: number | null) => (nano === null ? "-" : formatUsdTable(nano / 1e9, locale));
  return (
    <Dialog open={code !== null} onOpenChange={(open) => !open && onClose()}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t("adminBilling.codes.recordsTitle", { code: shown?.codeHint ?? "" })}</DialogTitle>
        </DialogHeader>
        <div className={`max-h-[50vh] overflow-auto rounded-lg border ${loading ? "opacity-60" : ""}`}>
          <table className="w-full text-sm">
            <thead className="sticky top-0 bg-muted/90 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="px-3 py-2">{t("adminBilling.codes.recordColumns.user")}</th>
                <th className="px-3 py-2">{t("adminBilling.codes.recordColumns.reward")}</th>
                <th className="px-3 py-2">{t("adminBilling.codes.recordColumns.time")}</th>
                <th className="px-3 py-2">{t("adminBilling.codes.recordColumns.balance")}</th>
              </tr>
            </thead>
            <tbody>
              {rows.map((r) => (
                <tr key={r.id} className="border-t">
                  <td className="px-3 py-2">{r.userLabel || r.userDisplayName || r.username || `#${r.userID}`}</td>
                  <td className="px-3 py-2">{r.rewardType === "plan" ? `${r.planName} · ${t("adminBilling.codes.days", { count: r.durationDays })}` : formatCredit(r.creditUSD, locale)}</td>
                  <td className="whitespace-nowrap px-3 py-2 text-muted-foreground">{formatDateTime(r.createdAt, locale)}</td>
                  <td className="whitespace-nowrap px-3 py-2 tabular-nums">
                    {money(r.balanceBeforeNanousd)} → {money(r.balanceAfterNanousd)}
                  </td>
                </tr>
              ))}
              {rows.length === 0 && !loading && failed ? (
                <tr>
                  <td colSpan={4} className="p-3">
                    <LoadError message={t("adminBilling.codes.recordsFailed")} onRetry={() => setAttempt((n) => n + 1)} />
                  </td>
                </tr>
              ) : null}
              {rows.length === 0 && !loading && !failed ? (
                <tr>
                  <td colSpan={4} className="h-20 text-center text-muted-foreground">
                    {t("adminBilling.codes.noRecords")}
                  </td>
                </tr>
              ) : null}
            </tbody>
          </table>
        </div>
        <Pagination page={page} pageSize={25} total={total} onPage={setPage} />
      </DialogContent>
    </Dialog>
  );
}
