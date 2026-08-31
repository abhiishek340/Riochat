"use client";

import { Download, Pencil, Upload } from "lucide-react";
import { useMemo, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { Tooltip } from "@/components/ui/tooltip";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { ModelIcon } from "../components/model-icon";
import { Section } from "../components/page";
import { Pagination } from "../components/pagination";
import { OptionSelect, SearchBox, Toolbar } from "../components/toolbar";
import { datedFileName, downloadJson } from "../lib/download";
import { formatDisplayMoney } from "../lib/format";
import type { BillingConfig, ModelPrice, PlatformModel } from "../lib/wire";
import { savePrice } from "./api";
import { exportPricing, filterRows, freeToggleBody, orderRows, parseImport, type ImportError, type PricingFilters } from "./pricing";
import { PricingDialog, type PricedRow } from "./pricing-dialog";

const PAGE_SIZE = 25;

/** Active models that have at least one active source can be priced. */
export function pricedRows(models: readonly PlatformModel[]): Array<PricedRow & { sortOrder: number }> {
  return models
    .filter((m) => m.status === "active" && m.activeSourceCount > 0)
    .map((m) => ({ name: m.platformModelName, vendor: m.vendor, vendorName: m.vendorName || m.vendor, icon: m.icon || m.vendorIcon, kindsJSON: m.kindsJSON, sortOrder: m.sortOrder }));
}

export function summaryOf(price: ModelPrice, money: (usd: number) => string, label: (mode: string) => string): string {
  if (price.isFree) return "";
  switch (price.pricingMode) {
    case "call":
      return `${label("call")} ${money(price.callUSDPerCall)}`;
    case "duration":
      return `${label("duration")} ${money(price.durationUSDPerSecond)}`;
    case "tiered":
      return label("tiered");
    default:
      return `${money(price.inputUSDPerMTokens)} / ${money(price.outputUSDPerMTokens)}`;
  }
}

export function PricingSection({ config, models, prices, onPrices }: { config: BillingConfig; models: PlatformModel[]; prices: ModelPrice[]; onPrices: (update: (prices: ModelPrice[]) => ModelPrice[]) => void }) {
  const t = useT();
  const { locale } = useI18n();
  const message = useErrorMessage();
  const input = useRef<HTMLInputElement>(null);
  const [page, setPage] = useState(1);
  const [filters, setFilters] = useState<PricingFilters>({ q: "", vendor: "", mode: "", free: "", configured: "" });
  const [editing, setEditing] = useState<PricedRow | null>(null);
  const [importing, setImporting] = useState(false);

  const rows = useMemo(() => pricedRows(models), [models]);
  const byName = useMemo(() => new Map(prices.map((p) => [p.platformModelName, p])), [prices]);
  const priced = useMemo(() => new Set(prices.map((p) => p.platformModelName)), [prices]);
  const ordered = useMemo(() => orderRows(rows, priced), [rows, priced]);
  const filtered = useMemo(() => filterRows(ordered, byName, filters), [ordered, byName, filters]);
  const vendors = useMemo(() => [...new Map(rows.map((r) => [r.vendor, r.vendorName])).entries()].sort((a, b) => a[1].localeCompare(b[1])), [rows]);
  const slice = filtered.slice((page - 1) * PAGE_SIZE, page * PAGE_SIZE);

  const money = (usd: number) => formatDisplayMoney(usd, { currency: config.displayCurrency, rate: config.usdToCNYRate }, locale, 4);
  const setFilter = <K extends keyof PricingFilters>(key: K, value: PricingFilters[K]) => {
    setFilters((f) => ({ ...f, [key]: value }));
    setPage(1);
  };

  const getKey = (r: PricedRow) => r.name;
  const upsert = (saved: ModelPrice) => onPrices((list) => (list.some((p) => p.platformModelName === saved.platformModelName) ? list.map((p) => (p.platformModelName === saved.platformModelName ? saved : p)) : [...list, saved]));

  // The free switch re-sends the whole record, one toggle at a time.
  const [toggling, setToggling] = useState<string | null>(null);
  const toggleFree = async (row: PricedRow, isFree: boolean) => {
    if (toggling) return;
    setToggling(row.name);
    const before = byName.get(row.name);
    if (before) onPrices((list) => list.map((p) => (p.platformModelName === row.name ? { ...p, isFree } : p)));
    try {
      upsert(await savePrice(freeToggleBody(row.name, before, isFree)));
    } catch (error) {
      if (before) onPrices((list) => list.map((p) => (p.platformModelName === row.name ? before : p)));
      toast.error(t("adminBilling.pricing.updateFailed"), { description: message(error) });
    } finally {
      setToggling(null);
    }
  };

  const exportAll = () => {
    if (prices.length === 0) return toast.info(t("adminBilling.pricing.nothingToExport"));
    downloadJson(datedFileName("riochat-model-pricing", "json"), exportPricing(prices));
  };

  const describe = (e: ImportError): string => {
    switch (e.kind) {
      case "unknownModels":
        return (e.more ?? 0) > 0 ? t("adminBilling.pricing.import.unknownMore", { names: (e.names ?? []).join(", "), more: e.more ?? 0 }) : t("adminBilling.pricing.import.unknown", { names: (e.names ?? []).join(", ") });
      default:
        return t(`adminBilling.pricing.import.errors.${e.kind}`, { name: e.name ?? "", field: e.field ?? "" });
    }
  };

  const runImport = async (file: File | undefined) => {
    if (input.current) input.current.value = "";
    if (!file) return;
    const known = new Map(rows.map((r) => [r.name, { kindsJSON: r.kindsJSON }]));
    const result = parseImport(await file.text(), known);
    if (!result.ok) {
      const first = result.errors[0];
      toast.error(t(first?.kind === "empty" ? "adminBilling.pricing.import.emptyTitle" : "adminBilling.pricing.import.invalidTitle"), { description: result.errors.slice(0, 3).map(describe).join(" ") });
      return;
    }
    setImporting(true);
    let done = 0;
    try {
      // Entries are saved one after another.
      for (const entry of result.entries) {
        upsert(await savePrice(entry));
        done++;
      }
      toast.success(t("adminBilling.pricing.import.done", { count: done }));
    } catch (error) {
      toast.error(t("adminBilling.pricing.import.failed", { done, total: result.entries.length }), { description: message(error) });
    } finally {
      setImporting(false);
    }
  };

  const label = (mode: string) => t(`adminBilling.pricing.modes.${mode === "call" || mode === "duration" || mode === "tiered" ? mode : "token"}`);
  const columns: Column<PricedRow>[] = [
    {
      id: "model",
      sticky: "left",
      header: t("adminBilling.pricing.columns.model"),
      cell: (r) => (
        <div className="flex min-w-0 items-center gap-2">
          <ModelIcon icon={r.icon} name={r.name} size={22} />
          <div className="min-w-0">
            <div className="truncate font-medium">{r.name}</div>
            <div className="truncate text-xs text-muted-foreground">{r.vendorName}</div>
          </div>
        </div>
      ),
    },
    {
      id: "price",
      header: t("adminBilling.pricing.columns.price"),
      cell: (r) => {
        const p = byName.get(r.name);
        if (!p) return <Badge tone="warning">{t("adminBilling.pricing.unconfigured")}</Badge>;
        return (
          <div className="space-y-0.5">
            <Badge tone="outline">{label(p.pricingMode)}</Badge>
            <div className="text-[13px] tabular-nums text-muted-foreground">{p.isFree ? t("adminBilling.pricing.freeLabel") : summaryOf(p, money, label)}</div>
          </div>
        );
      },
    },
    {
      id: "free",
      header: t("adminBilling.pricing.columns.free"),
      cell: (r) => <Switch checked={byName.get(r.name)?.isFree ?? false} disabled={toggling !== null} aria-label={`${t("adminBilling.pricing.free")}: ${r.name}`} onCheckedChange={(on) => void toggleFree(r, on)} />,
    },
    {
      id: "actions",
      sticky: "right",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (r) => (
        <CellActions>
          <Tooltip label={t("common.actions.edit")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.edit")}: ${r.name}`} onClick={() => setEditing(r)}>
              <Pencil />
            </Button>
          </Tooltip>
        </CellActions>
      ),
    },
  ];

  return (
    <Section
      title={t("adminBilling.pricing.title")}
      description={t("adminBilling.pricing.description")}
      actions={
        <>
          <input ref={input} type="file" accept="application/json,.json" hidden onChange={(e) => void runImport(e.target.files?.[0])} />
          <Button variant="outline" size="sm" onClick={exportAll}>
            <Download />
            {t("adminBilling.pricing.export")}
          </Button>
          <Button variant="outline" size="sm" loading={importing} onClick={() => input.current?.click()}>
            <Upload />
            {t("adminBilling.pricing.import.open")}
          </Button>
        </>
      }
      flush
    >
      <div className="space-y-3 p-4">
        <Toolbar>
          <SearchBox value={filters.q} onChange={(v) => setFilter("q", v)} placeholder={t("adminBilling.pricing.search")} />
          <OptionSelect value={filters.vendor} onChange={(v) => setFilter("vendor", v)} label={t("adminBilling.pricing.vendor")} options={[{ value: "", label: t("adminBilling.pricing.anyVendor") }, ...vendors.map(([key, name]) => ({ value: key, label: name }))]} />
          <OptionSelect value={filters.mode} onChange={(v) => setFilter("mode", v)} label={t("adminBilling.pricing.mode")} options={[{ value: "", label: t("adminBilling.pricing.anyMode") }, ...(["token", "call", "duration", "tiered"] as const).map((m) => ({ value: m, label: t(`adminBilling.pricing.modes.${m}`) }))]} />
          <OptionSelect value={filters.free} onChange={(v) => setFilter("free", v as PricingFilters["free"])} label={t("adminBilling.pricing.free")} options={[{ value: "", label: t("adminBilling.pricing.anyFree") }, { value: "free", label: t("adminBilling.pricing.onlyFree") }, { value: "paid", label: t("adminBilling.pricing.onlyPaid") }]} />
          <OptionSelect value={filters.configured} onChange={(v) => setFilter("configured", v as PricingFilters["configured"])} label={t("adminBilling.pricing.configured")} options={[{ value: "", label: t("adminBilling.pricing.anyConfigured") }, { value: "configured", label: t("adminBilling.pricing.onlyConfigured") }, { value: "unconfigured", label: t("adminBilling.pricing.onlyUnconfigured") }]} />
        </Toolbar>
        <DataTable columns={columns} rows={slice} rowKey={getKey} empty={t("adminBilling.pricing.empty")} label={t("adminBilling.pricing.title")} />
        <Pagination page={page} pageSize={PAGE_SIZE} total={filtered.length} onPage={setPage} />
      </div>
      <PricingDialog row={editing} price={editing ? byName.get(editing.name) : undefined} onOpenChange={(open) => !open && setEditing(null)} onSaved={upsert} />
    </Section>
  );
}
