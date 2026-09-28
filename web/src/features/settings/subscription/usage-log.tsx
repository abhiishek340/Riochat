"use client";

import { RefreshCw, Search } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input, NativeSelect } from "@/components/ui/input";
import { Table, TableBody, TableCell, TableEmptyRow, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { toast } from "@/components/ui/toast-store";
import { Tooltip } from "@/components/ui/tooltip";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useI18n, useT } from "@/lib/i18n/provider";
import { LoadErrorNotice, Pager, SettingsSection } from "../parts";
import { formatLatency, formatStamp } from "../lib/format";
import { useLoad } from "../lib/use-load";
import { fetchUsage, type UsageQuery } from "./api";
import { buildBreakdown, serviceLines, parseSnapshot, type Breakdown, type ServiceLine } from "./billing-breakdown";
import { BillingTip } from "./billing-tip";
import { formatMoney, type Display } from "./money";
import { groupLedger, LOG_SORTS, type LogRow } from "./usage-logic";
import type { LedgerItem } from "./types";

const PAGE_SIZES = [10, 25, 50, 100];
/** The formula tooltips open on focus too, so the cells that carry them take focus. */
const TIP_TRIGGER = "cursor-default rounded-sm underline decoration-dotted underline-offset-4 outline-none focus-visible:ring-2 focus-visible:ring-ring";

function factsOf(item: LedgerItem, extra: number) {
  return {
    isFreeModel: item.isFreeModel,
    inputTokens: item.inputTokens,
    outputTokens: item.outputTokens,
    reasoningTokens: item.reasoningTokens,
    cacheReadTokens: item.cacheReadTokens,
    cacheWriteTokens: item.cacheWriteTokens,
    callCount: item.callCount,
    durationSeconds: item.durationSeconds,
    billedNano: Math.round(item.billedUSD * 1e9) + extra,
  };
}

/** Service lines carried by the entries folded into a row. */
function attachedServices(row: LogRow): ServiceLine[] {
  return row.attached.flatMap((a) => {
    const lines = serviceLines(parseSnapshot(a.pricingSnapshotJSON));
    // An entry with items but no usable amounts still carries its own billed value.
    return lines.length ? lines : [{ name: "", calls: 1, unitNano: Math.round(a.billedUSD * 1e9), billedNano: Math.round(a.billedUSD * 1e9) }];
  });
}

function rowBreakdown(row: LogRow): { all: Breakdown; base: number; service: number } {
  const extra = attachedServices(row);
  const all = buildBreakdown(row.item.pricingSnapshotJSON, factsOf(row.item, 0), extra);
  // The entry's billed value is authoritative when the snapshot has no lines.
  const billedNano = Math.round(row.item.billedUSD * 1e9);
  const ownServices = all.services.length - extra.length;
  const ownServiceNano = all.services.slice(0, Math.max(0, ownServices)).reduce((s, l) => s + l.billedNano, 0);
  const base = all.lines.length > 0 ? all.baseNano : Math.max(0, billedNano - ownServiceNano);
  return { all, base: row.item.isFreeModel && all.serviceNano === 0 ? 0 : base, service: all.serviceNano };
}

export function UsageLog({ display, refreshKey }: { display: Display; refreshKey: number }) {
  const t = useT();
  const { locale } = useI18n();
  const errorMessage = useErrorMessage();
  const [pageSize, setPageSize] = useState(25);
  const [search, setSearch] = useState("");
  const [status, setStatus] = useState<UsageQuery["status"]>("");
  const [sort, setSort] = useState<string>("newest");
  const query = useDebouncedValue(search, 300);

  // Any filter change goes back to the first page. The page belongs to the
  // filters it was chosen under, so the very render that sees new filters
  // already asks for page 1: one request, not a stale page and then page 1.
  const filterKey = `${query}|${status}|${sort}|${pageSize}`;
  const [paging, setPaging] = useState({ page: 1, key: filterKey });
  const page = paging.key === filterKey ? paging.page : 1;
  const setPage = (next: number) => setPaging({ page: next, key: filterKey });

  const load = useLoad(
    (signal) => fetchUsage({ page, pageSize, query, status, sort }, signal),
    `${page}|${pageSize}|${query}|${status}|${sort}|${refreshKey}`,
  );
  useEffect(() => {
    if (load.error) toast.error(t("settings.billing.log.loadFailed"), { description: errorMessage(load.error) });
    // One toast per failure.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [load.error]);

  const rows = useMemo(() => groupLedger(load.data?.items ?? []), [load.data]);
  const failed = load.error !== undefined && !load.data;
  const total = load.data?.total ?? 0;
  const loading = load.loading;

  return (
    <SettingsSection title={t("settings.billing.log.title")} bare>
      <div className="grid gap-3 rounded-xl border bg-card p-4 shadow-sm">
        <div className="flex flex-wrap items-center gap-2">
          <div className="relative min-w-48 flex-1">
            <Search className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
            <Input
              className="pl-8"
              value={search}
              placeholder={t("settings.billing.log.search")}
              aria-label={t("settings.billing.log.search")}
              onChange={(e) => setSearch(e.target.value)}
            />
          </div>
          <NativeSelect className="w-auto" aria-label={t("settings.billing.log.type")} value={status} onChange={(e) => setStatus(e.target.value as UsageQuery["status"])}>
            <option value="">{t("settings.billing.log.types.all")}</option>
            <option value="free">{t("settings.billing.log.types.free")}</option>
            <option value="billable">{t("settings.billing.log.types.billable")}</option>
          </NativeSelect>
          <NativeSelect className="w-auto" aria-label={t("settings.billing.log.sort")} value={sort} onChange={(e) => setSort(e.target.value)}>
            {LOG_SORTS.map((s) => (
              <option key={s} value={s}>
                {t(`settings.billing.log.sorts.${s}`)}
              </option>
            ))}
          </NativeSelect>
          <Button variant="outline" size="icon" aria-label={t("common.actions.refresh")} disabled={loading} onClick={load.reload}>
            <RefreshCw className={loading ? "animate-spin" : undefined} />
          </Button>
        </div>

        {failed ? <LoadErrorNotice title={t("settings.billing.log.loadFailed")} error={load.error} onRetry={load.reload} /> : null}

        <Table aria-label={t("settings.billing.log.title")}>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead>{t("settings.billing.log.columns.time")}</TableHead>
              <TableHead>{t("settings.billing.log.columns.model")}</TableHead>
              <TableHead className="text-right">{t("settings.billing.log.columns.base")}</TableHead>
              <TableHead className="text-right">{t("settings.billing.log.columns.service")}</TableHead>
              <TableHead className="text-right">{t("settings.billing.log.columns.balance")}</TableHead>
              <TableHead className="text-right">{t("settings.billing.log.columns.latency")}</TableHead>
            </TableRow>
          </TableHeader>
          <TableBody>
            {loading && !load.data ? (
              <TableEmptyRow colSpan={6}>{t("common.states.loading")}</TableEmptyRow>
            ) : failed ? (
              <TableEmptyRow colSpan={6}>—</TableEmptyRow>
            ) : rows.length === 0 ? (
              <TableEmptyRow colSpan={6}>{t("settings.billing.log.empty")}</TableEmptyRow>
            ) : (
              rows.map((row) => {
                const { all, base, service } = rowBreakdown(row);
                return (
                  <TableRow key={row.item.id} className={loading ? "opacity-60" : undefined}>
                    <TableCell className="whitespace-nowrap tabular-nums text-muted-foreground">{formatStamp(row.item.billingAt || row.item.createdAt)}</TableCell>
                    <TableCell className="max-w-56 truncate font-medium">{row.item.platformModelName || "-"}</TableCell>
                    <TableCell className="text-right tabular-nums">
                      <Tooltip label={<BillingTip breakdown={all} display={display} scope="all" />} side="left" className="max-w-sm">
                        <span tabIndex={0} className={TIP_TRIGGER}>
                          {formatMoney(base / 1e9, display, locale)}
                        </span>
                      </Tooltip>
                    </TableCell>
                    <TableCell className="text-right tabular-nums">
                      {all.services.length > 0 ? (
                        <Tooltip label={<BillingTip breakdown={all} display={display} scope="services" />} side="left" className="max-w-sm">
                          <span tabIndex={0} className={TIP_TRIGGER}>
                            {formatMoney(service / 1e9, display, locale)}
                          </span>
                        </Tooltip>
                      ) : (
                        formatMoney(0, display, locale)
                      )}
                    </TableCell>
                    <TableCell className="text-right tabular-nums">{row.item.balanceAfterUSD === null ? "-" : formatMoney(row.item.balanceAfterUSD, display, locale)}</TableCell>
                    <TableCell className="text-right tabular-nums">{formatLatency(row.item.latencyMS)}</TableCell>
                  </TableRow>
                );
              })
            )}
          </TableBody>
        </Table>

        <Pager page={page} pageSize={pageSize} total={total} sizes={PAGE_SIZES} disabled={loading} onPage={setPage} onPageSize={setPageSize} />
      </div>
    </SettingsSection>
  );
}
