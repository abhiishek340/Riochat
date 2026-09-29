"use client";

import { Copy } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Tooltip } from "@/components/ui/tooltip";
import { toast } from "@/components/ui/toast-store";
import { isAbortError } from "@/lib/api/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import { copyText } from "../components/copy-button";
import { DetailList, DetailSection } from "../components/detail-list";
import { JsonView } from "../components/json-view";
import { ModelIcon } from "../components/model-icon";
import { Mono } from "../components/page";
import { StatusBadge } from "../components/status";
import { OptionSelect, SearchBox } from "../components/toolbar";
import type { Column } from "../components/data-table";
import { formatBytes, formatCents, formatCredit, formatDateTime, formatDisplayMoney, formatLatency, formatNumber, formatUsdTable } from "../lib/format";
import { loadAllModels, loadBillingConfig } from "../lib/reference";
import { useLabel } from "../lib/use-label";
import { useNotify } from "../lib/use-notify";
import { usePagedQuery } from "../lib/use-paged-query";
import { DEFAULT_BILLING_CONFIG, type BillingConfig, type PlatformModel } from "../lib/wire";
import {
  getConversationEvent,
  listAudit,
  listAuthEvents,
  listCalls,
  listConversationEvents,
  listErrors,
  listOrders,
  listRedemptions,
  type AuditLog,
  type AuthEvent,
  type CallLog,
  type ConversationEvent,
  type ErrorLog,
  type PaymentOrder,
  type RedemptionRecord,
} from "./api";
import { billableOutput, breakdownOf, totalTokens, upstreamUsageOf } from "./breakdown";
import { DateRangeFilter, userText } from "./filters";
import { LogView } from "./log-view";
import { AUDIT_ACTIONS, AUDIT_RESOURCES, dateRange, distinct, searchParams, sortAuthEvents, withObserved, type AuthSort } from "./query";

export interface TabProps {
  remountKey: number;
}

const loadFailed = (tab: "audit" | "auth" | "usage" | "orders" | "redemptions" | "conversation" | "errors") => `adminLogs.loadFailed.${tab}` as const;

function useBillingConfig(): BillingConfig {
  const [config, setConfig] = useState<BillingConfig>(DEFAULT_BILLING_CONFIG);
  useEffect(() => {
    void loadBillingConfig().then(setConfig, () => undefined);
  }, []);
  return config;
}

function time(iso: string | null | undefined, locale: string) {
  return <span className="whitespace-nowrap text-muted-foreground">{formatDateTime(iso, locale)}</span>;
}

// --- audit -----------------------------------------------------------------------------

export function AuditTab({ remountKey }: TabProps) {
  const t = useT();
  const { locale } = useI18n();
  const label = useLabel();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [resource, setResource] = useState("");
  const [action, setAction] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [sort, setSort] = useState("id_desc");
  const filters = useMemo(() => ({ q, resource, action, from, to, sort, remountKey }), [q, resource, action, from, to, sort, remountKey]);
  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) =>
      listAudit({ page, page_size: pageSize, ...searchParams(f.q, "actor_user_id"), resource: f.resource, action: f.action, ...dateRange(f.from, f.to), sort: f.sort }, signal),
    onError: (error) => notify.error(loadFailed("audit"), error),
  });
  const resources = withObserved(AUDIT_RESOURCES, distinct(list.rows.map((r) => r.resource)));
  const actions = withObserved(AUDIT_ACTIONS, distinct(list.rows.map((r) => r.action)));
  const columns: Column<AuditLog>[] = [
    { id: "time", header: t("adminLogs.audit.columns.time"), cell: (r) => time(r.createdAt, locale) },
    { id: "actor", header: t("adminLogs.audit.columns.actor"), cell: (r) => <span className="font-medium">{r.actorLabel || r.actorDisplayName || r.actorUsername || `#${r.actorUserID}`}</span> },
    { id: "action", header: t("adminLogs.audit.columns.action"), cell: (r) => label("adminLogs.audit.actions", r.action) },
    { id: "resource", header: t("adminLogs.audit.columns.resource"), cell: (r) => (r.resource ? `${label("adminLogs.audit.resources", r.resource)}${r.resourceID ? ` · ${r.resourceID}` : ""}` : "-") },
    { id: "ip", header: t("adminLogs.audit.columns.ip"), cell: (r) => <Mono className="text-muted-foreground">{r.ip || "-"}</Mono> },
  ];
  return (
    <LogView
      list={list}
      columns={columns}
      rowKey={(r) => r.id}
      label={t("adminLogs.tabs.audit")}
      empty={t("adminLogs.empty")}
      detailTitle={(r) => label("adminLogs.audit.actions", r.action)}
      filters={
        <>
          <SearchBox value={q} onChange={setQ} placeholder={t("adminLogs.audit.search")} />
          <OptionSelect value={resource} onChange={setResource} label={t("adminLogs.audit.columns.resource")} options={[{ value: "", label: t("adminLogs.audit.anyResource") }, ...resources.map((r) => ({ value: r, label: label("adminLogs.audit.resources", r) }))]} />
          <OptionSelect value={action} onChange={setAction} label={t("adminLogs.audit.columns.action")} options={[{ value: "", label: t("adminLogs.audit.anyAction") }, ...actions.map((a) => ({ value: a, label: label("adminLogs.audit.actions", a) }))]} />
          <DateRangeFilter from={from} to={to} onFrom={setFrom} onTo={setTo} />
          <OptionSelect value={sort} onChange={setSort} label={t("adminLogs.sort")} options={["id_desc", "id_asc", "created_desc", "created_asc"].map((s) => ({ value: s, label: t(`adminLogs.audit.sorts.${s}` as "adminLogs.audit.sorts.id_desc") }))} />
        </>
      }
      detail={(r) => (
        <div className="space-y-5">
          <DetailList
            items={[
              [t("adminLogs.audit.columns.time"), formatDateTime(r.createdAt, locale)],
              [t("adminLogs.audit.columns.actor"), `${r.actorLabel || r.actorUsername} (#${r.actorUserID})`],
              [t("adminLogs.audit.columns.action"), label("adminLogs.audit.actions", r.action)],
              [t("adminLogs.audit.columns.resource"), `${label("adminLogs.audit.resources", r.resource)} ${r.resourceID}`.trim()],
              [t("adminLogs.fields.ip"), r.ip],
              [t("adminLogs.fields.userAgent"), r.userAgent],
              [t("adminLogs.fields.requestId"), r.requestID ? <Mono key="r">{r.requestID}</Mono> : ""],
            ]}
          />
          {r.detailJSON ? (
            <DetailSection title={t("adminLogs.fields.detail")}>
              <JsonView text={r.detailJSON} />
            </DetailSection>
          ) : null}
        </div>
      )}
    />
  );
}

// --- security events ---------------------------------------------------------------------

export function AuthTab({ remountKey }: TabProps) {
  const t = useT();
  const { locale } = useI18n();
  const label = useLabel();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [result, setResult] = useState("");
  const [sort, setSort] = useState<AuthSort>("occurred_desc");
  const filters = useMemo(() => ({ q, result, remountKey }), [q, result, remountKey]);
  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) => listAuthEvents({ page, page_size: pageSize, ...searchParams(f.q, "user_id", "event_type"), result: f.result }, signal),
    onError: (error) => notify.error(loadFailed("auth"), error),
  });
  const rows = useMemo(() => sortAuthEvents(list.rows, sort), [list.rows, sort]);
  const columns: Column<AuthEvent>[] = [
    { id: "time", header: t("adminLogs.auth.columns.time"), cell: (r) => time(r.occurredAt, locale) },
    { id: "user", header: t("adminLogs.auth.columns.user"), cell: (r) => userText(r) },
    { id: "event", header: t("adminLogs.auth.columns.event"), cell: (r) => <Mono>{r.eventType}</Mono> },
    { id: "result", header: t("adminLogs.auth.columns.result"), cell: (r) => <StatusBadge value={r.result} prefix="adminLogs.results" /> },
    { id: "reason", header: t("adminLogs.auth.columns.reason"), cell: (r) => <span className="text-muted-foreground">{r.reason || "-"}</span> },
    { id: "ip", header: t("adminLogs.fields.ip"), cell: (r) => <Mono className="text-muted-foreground">{r.clientIP || "-"}</Mono> },
  ];
  return (
    <LogView
      list={list}
      rows={rows}
      columns={columns}
      rowKey={(r) => r.id}
      label={t("adminLogs.tabs.auth")}
      empty={t("adminLogs.empty")}
      detailTitle={(r) => r.eventType}
      filters={
        <>
          <SearchBox value={q} onChange={setQ} placeholder={t("adminLogs.auth.search")} />
          <OptionSelect value={result} onChange={setResult} label={t("adminLogs.auth.columns.result")} options={[{ value: "", label: t("adminLogs.auth.anyResult") }, ...["success", "failure", "blocked"].map((v) => ({ value: v, label: label("adminLogs.results", v) }))]} />
          <OptionSelect value={sort} onChange={(v) => setSort(v as AuthSort)} label={t("adminLogs.sort")} options={(["occurred_desc", "occurred_asc", "id_desc", "id_asc"] as const).map((s) => ({ value: s, label: t(`adminLogs.auth.sorts.${s}`) }))} />
        </>
      }
      detail={(r) => (
        <div className="space-y-5">
          <DetailList
            items={[
              [t("adminLogs.auth.columns.time"), formatDateTime(r.occurredAt, locale)],
              [t("adminLogs.auth.columns.user"), `${userText(r)} (#${r.userID})`],
              [t("adminLogs.auth.columns.event"), r.eventType],
              [t("adminLogs.auth.columns.result"), label("adminLogs.results", r.result)],
              [t("adminLogs.auth.columns.reason"), r.reason],
              [t("adminLogs.fields.ip"), r.clientIP],
              [t("adminLogs.fields.userAgent"), r.userAgent],
              [t("adminLogs.fields.requestId"), r.requestID],
            ]}
          />
          {r.detailJSON ? (
            <DetailSection title={t("adminLogs.fields.detail")}>
              <JsonView text={r.detailJSON} />
            </DetailSection>
          ) : null}
        </div>
      )}
    />
  );
}

// --- usage (call logs) -----------------------------------------------------------------------

const CALL_SORTS = ["created_desc", "created_asc", "cost_desc", "tokens_desc", "latency_desc"] as const;
const BILLING_MODES = ["free", "token", "call", "duration", "tiered"] as const;

export function UsageTab({ remountKey }: TabProps) {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const config = useBillingConfig();
  const [q, setQ] = useState("");
  const [model, setModel] = useState("");
  const [billing, setBilling] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [sort, setSort] = useState("created_desc");
  const [models, setModels] = useState<PlatformModel[]>([]);
  useEffect(() => {
    void loadAllModels().then(setModels, () => undefined);
  }, []);
  const filters = useMemo(() => ({ q, model, billing, from, to, sort, remountKey }), [q, model, billing, from, to, sort, remountKey]);
  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) =>
      listCalls({ page, page_size: pageSize, ...searchParams(f.q, "user_id"), platform_model_name: f.model, billing_mode: f.billing, ...dateRange(f.from, f.to), sort: f.sort }, signal),
    onError: (error) => notify.error(loadFailed("usage"), error),
  });
  const money = (usd: number) => formatDisplayMoney(usd, { currency: config.displayCurrency, rate: config.usdToCNYRate }, locale, 4);
  const modelOptions = distinct(models.map((m) => m.platformModelName));
  const columns: Column<CallLog>[] = [
    { id: "time", header: t("adminLogs.usage.columns.time"), cell: (r) => time(r.createdAt, locale) },
    { id: "user", header: t("adminLogs.usage.columns.user"), cell: (r) => userText(r) },
    {
      id: "model",
      header: t("adminLogs.usage.columns.model"),
      cell: (r) => (
        <div className="flex min-w-0 items-center gap-2">
          <ModelIcon icon={models.find((m) => m.platformModelName === r.platformModelName)?.icon ?? r.modelIcon} name={r.platformModelName} size={18} />
          <div className="min-w-0">
            <div className="truncate font-medium">{r.platformModelName}</div>
            <div className="truncate text-xs text-muted-foreground">{r.upstreamName}</div>
          </div>
        </div>
      ),
    },
    { id: "tokens", header: t("adminLogs.usage.columns.tokens"), className: "tabular-nums", cell: (r) => formatNumber(totalTokens(r), locale) },
    { id: "latency", header: t("adminLogs.usage.columns.latency"), className: "whitespace-nowrap tabular-nums", cell: (r) => formatLatency(r.latencyMS, locale) },
    { id: "cost", header: t("adminLogs.usage.columns.cost"), className: "whitespace-nowrap tabular-nums", cell: (r) => <CostCell row={r} money={money} /> },
  ];
  return (
    <LogView
      list={list}
      columns={columns}
      rowKey={(r) => r.id}
      label={t("adminLogs.tabs.usage")}
      empty={t("adminLogs.empty")}
      detailTitle={(r) => r.platformModelName}
      filters={
        <>
          <SearchBox value={q} onChange={setQ} placeholder={t("adminLogs.usage.search")} />
          <OptionSelect value={model} onChange={setModel} label={t("adminLogs.usage.columns.model")} options={[{ value: "", label: t("adminLogs.usage.anyModel") }, ...modelOptions.map((m) => ({ value: m, label: m }))]} />
          <OptionSelect value={billing} onChange={setBilling} label={t("adminLogs.usage.billing")} options={[{ value: "", label: t("adminLogs.usage.anyBilling") }, ...BILLING_MODES.map((m) => ({ value: m, label: t(`adminLogs.usage.modes.${m}`) }))]} />
          <DateRangeFilter from={from} to={to} onFrom={setFrom} onTo={setTo} />
          <OptionSelect value={sort} onChange={setSort} label={t("adminLogs.sort")} options={CALL_SORTS.map((s) => ({ value: s, label: t(`adminLogs.usage.sorts.${s}`) }))} />
        </>
      }
      detail={(r) => <UsageDetail row={r} money={money} />}
    />
  );
}

function CostCell({ row, money }: { row: CallLog; money: (usd: number) => string }) {
  const t = useT();
  const b = breakdownOf(row.pricingSnapshotJSON, row, row.billedUSD);
  const text = row.isFreeModel && row.billedUSD === 0 ? t("adminLogs.usage.free") : money(row.billedUSD);
  if (!b) return <span>{text}</span>;
  return (
    <Tooltip label={<BreakdownTip row={row} money={money} />} className="max-w-80">
      <span className="cursor-help border-b border-dotted border-muted-foreground/50">{text}</span>
    </Tooltip>
  );
}

function BreakdownTip({ row, money }: { row: CallLog; money: (usd: number) => string }) {
  const t = useT();
  const { locale } = useI18n();
  const b = breakdownOf(row.pricingSnapshotJSON, row, row.billedUSD);
  if (!b) return null;
  return (
    <div className="space-y-1 text-left">
      {b.free ? <div>{t("adminLogs.usage.breakdown.freeNote")}</div> : null}
      {b.rows.map((r, i) => (
        <div key={i} className="flex justify-between gap-4 tabular-nums">
          <span>{r.kind === "service" ? (r.name ?? t("adminLogs.usage.breakdown.service")) : t(`adminLogs.usage.breakdown.${r.kind}`)}</span>
          <span>{money(r.billedUSD)}</span>
        </div>
      ))}
      {b.rateMultiplier !== null && b.rateMultiplier !== 1 ? <div>{t("adminLogs.usage.breakdown.multiplier", { value: formatNumber(b.rateMultiplier, locale, 2) })}</div> : null}
      {b.schedule ? <div>{t("adminLogs.usage.breakdown.schedule", { name: b.schedule.name || "-", percent: b.schedule.percent })}</div> : null}
      {b.tier ? <div>{t("adminLogs.usage.breakdown.tier", { from: formatNumber(b.tier.from, locale), upTo: b.tier.upTo === null ? "∞" : formatNumber(b.tier.upTo, locale) })}</div> : null}
      {b.fastMode ? <div>{t("adminLogs.usage.breakdown.fast")}</div> : null}
      {b.serviceTier ? <div>{t("adminLogs.usage.breakdown.serviceTier", { tier: b.serviceTier })}</div> : null}
    </div>
  );
}

function UsageDetail({ row, money }: { row: CallLog; money: (usd: number) => string }) {
  const t = useT();
  const { locale } = useI18n();
  const raw = upstreamUsageOf(row.pricingSnapshotJSON);
  const b = breakdownOf(row.pricingSnapshotJSON, row, row.billedUSD);
  return (
    <div className="space-y-5">
      <DetailList
        items={[
          [t("adminLogs.usage.columns.time"), formatDateTime(row.createdAt, locale)],
          [t("adminLogs.usage.columns.user"), `${userText(row)} (#${row.userID})`],
          [t("adminLogs.usage.columns.model"), row.platformModelName],
          [t("adminLogs.usage.upstream"), `${row.upstreamName} / ${row.upstreamModelName}`],
          [t("adminLogs.usage.protocol"), row.providerProtocol],
          [t("adminLogs.usage.tokensIn"), formatNumber(row.inputTokens, locale)],
          [t("adminLogs.usage.tokensCacheRead"), formatNumber(row.cacheReadTokens, locale)],
          [t("adminLogs.usage.tokensCacheWrite"), formatNumber(row.cacheWriteTokens, locale)],
          [t("adminLogs.usage.tokensOut"), formatNumber(billableOutput(row), locale)],
          [t("adminLogs.usage.columns.tokens"), formatNumber(totalTokens(row), locale)],
          [t("adminLogs.usage.columns.latency"), formatLatency(row.latencyMS, locale)],
          [t("adminLogs.usage.columns.cost"), row.isFreeModel && row.billedUSD === 0 ? t("adminLogs.usage.free") : money(row.billedUSD)],
          row.balanceAfterUSD !== null ? [t("adminLogs.usage.balanceAfter"), money(row.balanceAfterUSD)] : null,
          [t("adminLogs.fields.conversation"), row.conversationID ? `#${row.conversationID}` : ""],
        ]}
      />
      {b && b.rows.length > 0 ? (
        <DetailSection title={t("adminLogs.usage.breakdown.title")}>
          <BreakdownTip row={row} money={money} />
        </DetailSection>
      ) : null}
      {raw !== undefined ? (
        <DetailSection title={t("adminLogs.usage.rawUsage")}>
          <JsonView text={JSON.stringify(raw)} />
        </DetailSection>
      ) : null}
    </div>
  );
}

// --- payment orders ---------------------------------------------------------------------------

const ORDER_SORTS = ["created_desc", "created_asc", "paid_desc", "amount_desc"] as const;

export function OrdersTab({ remountKey }: TabProps) {
  const t = useT();
  const { locale } = useI18n();
  const label = useLabel();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [orderType, setOrderType] = useState("");
  const [provider, setProvider] = useState("");
  const [status, setStatus] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [sort, setSort] = useState("created_desc");
  const filters = useMemo(() => ({ q, orderType, provider, status, from, to, sort, remountKey }), [q, orderType, provider, status, from, to, sort, remountKey]);
  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) =>
      listOrders({ page, page_size: pageSize, ...searchParams(f.q, "user_id"), order_type: f.orderType, provider: f.provider, status: f.status, ...dateRange(f.from, f.to), sort: f.sort }, signal),
    onError: (error) => notify.error(loadFailed("orders"), error),
  });
  const columns: Column<PaymentOrder>[] = [
    { id: "no", header: t("adminLogs.orders.columns.order"), cell: (r) => <Mono>{r.orderNo}</Mono> },
    { id: "user", header: t("adminLogs.orders.columns.user"), cell: (r) => userText(r) },
    { id: "type", header: t("adminLogs.orders.columns.type"), cell: (r) => label("adminLogs.orders.types", r.orderType) },
    { id: "provider", header: t("adminLogs.orders.columns.provider"), cell: (r) => label("adminLogs.orders.providers", r.provider) },
    { id: "amount", header: t("adminLogs.orders.columns.amount"), className: "whitespace-nowrap tabular-nums", cell: (r) => formatCents(r.payAmountCents, r.payCurrency, locale) },
    { id: "status", header: t("adminLogs.orders.columns.status"), cell: (r) => <StatusBadge value={r.status} prefix="adminLogs.orders.statuses" /> },
    { id: "created", header: t("adminLogs.orders.columns.created"), cell: (r) => time(r.createdAt, locale) },
  ];
  return (
    <LogView
      list={list}
      columns={columns}
      rowKey={(r) => r.id}
      label={t("adminLogs.tabs.orders")}
      empty={t("adminLogs.empty")}
      detailTitle={(r) => r.orderNo}
      filters={
        <>
          <SearchBox value={q} onChange={setQ} placeholder={t("adminLogs.orders.search")} />
          <OptionSelect value={orderType} onChange={setOrderType} label={t("adminLogs.orders.columns.type")} options={[{ value: "", label: t("adminLogs.orders.anyType") }, ...["subscription", "topup"].map((v) => ({ value: v, label: label("adminLogs.orders.types", v) }))]} />
          <OptionSelect value={provider} onChange={setProvider} label={t("adminLogs.orders.columns.provider")} options={[{ value: "", label: t("adminLogs.orders.anyProvider") }, ...["stripe", "epay"].map((v) => ({ value: v, label: label("adminLogs.orders.providers", v) }))]} />
          <OptionSelect value={status} onChange={setStatus} label={t("adminLogs.orders.columns.status")} options={[{ value: "", label: t("adminLogs.orders.anyStatus") }, ...["pending", "paid", "failed", "expired", "canceled", "refunded"].map((v) => ({ value: v, label: label("adminLogs.orders.statuses", v) }))]} />
          <DateRangeFilter from={from} to={to} onFrom={setFrom} onTo={setTo} />
          <OptionSelect value={sort} onChange={setSort} label={t("adminLogs.sort")} options={ORDER_SORTS.map((s) => ({ value: s, label: t(`adminLogs.orders.sorts.${s}`) }))} />
        </>
      }
      detail={(r) => (
        <div className="space-y-5">
          <DetailList
            items={[
              [t("adminLogs.orders.columns.order"), <Mono key="n">{r.orderNo}</Mono>],
              [t("adminLogs.orders.columns.user"), `${userText(r)} (#${r.userID})`],
              [t("adminLogs.orders.columns.type"), label("adminLogs.orders.types", r.orderType)],
              [t("adminLogs.orders.columns.provider"), label("adminLogs.orders.providers", r.provider)],
              [t("adminLogs.orders.columns.status"), label("adminLogs.orders.statuses", r.status)],
              [t("adminLogs.orders.pay"), formatCents(r.payAmountCents, r.payCurrency, locale)],
              [t("adminLogs.orders.base"), formatCents(r.baseAmountCents, r.baseCurrency, locale)],
              [t("adminLogs.orders.fx"), r.fxRate],
              [t("adminLogs.orders.credit"), r.creditUSD ? formatCredit(r.creditUSD, locale) : ""],
              [t("adminLogs.orders.interval"), r.billingInterval ? `${r.billingInterval} × ${r.cycles}` : ""],
              [t("adminLogs.orders.paidAt"), formatDateTime(r.paidAt, locale)],
              [t("adminLogs.orders.expiredAt"), formatDateTime(r.expiredAt, locale)],
              [t("adminLogs.orders.externalPayment"), r.externalPaymentID],
              [t("adminLogs.orders.externalCheckout"), r.externalCheckoutID],
            ]}
          />
          {r.snapshotJSON ? (
            <DetailSection title={t("adminLogs.fields.detail")}>
              <JsonView text={r.snapshotJSON} />
            </DetailSection>
          ) : null}
        </div>
      )}
    />
  );
}

// --- redemption records -------------------------------------------------------------------------

export function RedemptionsTab({ remountKey, codeId }: TabProps & { codeId?: number }) {
  const t = useT();
  const { locale } = useI18n();
  const label = useLabel();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [reward, setReward] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [sort, setSort] = useState("created_desc");
  const filters = useMemo(() => ({ q, reward, from, to, sort, codeId, remountKey }), [q, reward, from, to, sort, codeId, remountKey]);
  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) =>
      listRedemptions({ page, page_size: pageSize, ...searchParams(f.q, "user_id"), code_id: f.codeId, reward_type: f.reward, ...dateRange(f.from, f.to), sort: f.sort }, signal),
    onError: (error) => notify.error(loadFailed("redemptions"), error),
  });
  const columns: Column<RedemptionRecord>[] = [
    { id: "time", header: t("adminLogs.redemptions.columns.time"), cell: (r) => time(r.createdAt, locale) },
    { id: "code", header: t("adminLogs.redemptions.columns.code"), cell: (r) => <Mono>{r.codeHint}</Mono> },
    { id: "user", header: t("adminLogs.redemptions.columns.user"), cell: (r) => userText(r) },
    { id: "reward", header: t("adminLogs.redemptions.columns.reward"), cell: (r) => (r.rewardType === "plan" || r.planID ? `${r.planName || r.planID} · ${t("adminLogs.redemptions.days", { count: r.durationDays })}` : formatCredit(r.creditUSD, locale)) },
  ];
  return (
    <LogView
      list={list}
      columns={columns}
      rowKey={(r) => r.id}
      label={t("adminLogs.tabs.redemptions")}
      empty={t("adminLogs.empty")}
      detailTitle={(r) => r.codeHint}
      filters={
        <>
          <SearchBox value={q} onChange={setQ} placeholder={t("adminLogs.redemptions.search")} />
          <OptionSelect value={reward} onChange={setReward} label={t("adminLogs.redemptions.columns.reward")} options={[{ value: "", label: t("adminLogs.redemptions.anyReward") }, ...["credit", "plan"].map((v) => ({ value: v, label: label("adminLogs.redemptions.rewards", v) }))]} />
          <DateRangeFilter from={from} to={to} onFrom={setFrom} onTo={setTo} />
          <OptionSelect value={sort} onChange={setSort} label={t("adminLogs.sort")} options={["created_desc", "created_asc"].map((s) => ({ value: s, label: t(`adminLogs.redemptions.sorts.${s}` as "adminLogs.redemptions.sorts.created_desc") }))} />
        </>
      }
      detail={(r) => (
        <div className="space-y-5">
          <DetailList
            items={[
              [t("adminLogs.redemptions.columns.time"), formatDateTime(r.createdAt, locale)],
              [t("adminLogs.redemptions.columns.code"), <Mono key="c">{r.codeHint}</Mono>],
              [t("adminLogs.redemptions.codeStatus"), r.codeStatus],
              [t("adminLogs.redemptions.columns.user"), `${userText(r)} (#${r.userID})`],
              [t("adminLogs.redemptions.mode"), r.mode],
              [t("adminLogs.redemptions.columns.reward"), label("adminLogs.redemptions.rewards", r.rewardType)],
              [t("adminLogs.redemptions.credit"), r.creditUSD ? formatCredit(r.creditUSD, locale) : ""],
              [t("adminLogs.redemptions.plan"), r.planName],
              [t("adminLogs.redemptions.balanceBefore"), r.balanceBeforeNanousd === null ? "" : formatUsdTable(r.balanceBeforeNanousd / 1e9, locale)],
              [t("adminLogs.redemptions.balanceAfter"), r.balanceAfterNanousd === null ? "" : formatUsdTable(r.balanceAfterNanousd / 1e9, locale)],
              [t("adminLogs.redemptions.ref"), r.refNo],
              [t("adminLogs.redemptions.note"), r.codeDescription],
            ]}
          />
          {r.snapshotJSON ? (
            <DetailSection title={t("adminLogs.fields.detail")}>
              <JsonView text={r.snapshotJSON} />
            </DetailSection>
          ) : null}
        </div>
      )}
    />
  );
}

// --- conversation events ------------------------------------------------------------------------------

export function ConversationTab({ remountKey }: TabProps) {
  const t = useT();
  const { locale } = useI18n();
  const label = useLabel();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [conversation, setConversation] = useState("");
  const [eventType, setEventType] = useState("");
  const [status, setStatus] = useState("");
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [sort, setSort] = useState("created_desc");
  const filters = useMemo(() => ({ q, conversation, eventType, status, from, to, sort, remountKey }), [q, conversation, eventType, status, from, to, sort, remountKey]);
  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) =>
      listConversationEvents({ page, page_size: pageSize, ...searchParams(f.q, "user_id"), conversation_id: /^\d+$/.test(f.conversation.trim()) ? f.conversation.trim() : undefined, event_type: f.eventType, status: f.status, ...dateRange(f.from, f.to), sort: f.sort }, signal),
    onError: (error) => notify.error(loadFailed("conversation"), error),
  });
  const types = distinct(list.rows.map((r) => r.eventType));
  const columns: Column<ConversationEvent>[] = [
    { id: "time", header: t("adminLogs.conversation.columns.time"), cell: (r) => time(r.startedAt || r.createdAt, locale) },
    { id: "user", header: t("adminLogs.conversation.columns.user"), cell: (r) => userText(r) },
    { id: "type", header: t("adminLogs.conversation.columns.type"), cell: (r) => <Mono>{r.eventType}</Mono> },
    { id: "summary", header: t("adminLogs.conversation.columns.summary"), className: "max-w-[22rem]", cell: (r) => <span className="line-clamp-2 text-[13px]">{r.title || r.summary || r.toolName || "-"}</span> },
    { id: "status", header: t("adminLogs.conversation.columns.status"), cell: (r) => <StatusBadge value={r.status} prefix="adminLogs.results" /> },
    { id: "latency", header: t("adminLogs.conversation.columns.latency"), className: "whitespace-nowrap tabular-nums", cell: (r) => formatLatency(r.latencyMS, locale) },
  ];
  return (
    <LogView
      list={list}
      columns={columns}
      rowKey={(r) => r.id}
      label={t("adminLogs.tabs.conversation")}
      empty={t("adminLogs.empty")}
      detailTitle={(r) => r.title || r.eventType}
      filters={
        <>
          <SearchBox value={q} onChange={setQ} placeholder={t("adminLogs.conversation.search")} />
          <SearchBox value={conversation} onChange={setConversation} placeholder={t("adminLogs.conversation.conversationId")} className="sm:w-44" />
          <OptionSelect value={eventType} onChange={setEventType} label={t("adminLogs.conversation.columns.type")} options={[{ value: "", label: t("adminLogs.conversation.anyType") }, ...types.map((v) => ({ value: v, label: v }))]} />
          <OptionSelect value={status} onChange={setStatus} label={t("adminLogs.conversation.columns.status")} options={[{ value: "", label: t("adminLogs.conversation.anyStatus") }, ...withObserved(["success", "failed", "running"], distinct(list.rows.map((r) => r.status))).map((v) => ({ value: v, label: label("adminLogs.results", v) }))]} />
          <DateRangeFilter from={from} to={to} onFrom={setFrom} onTo={setTo} />
          <OptionSelect value={sort} onChange={setSort} label={t("adminLogs.sort")} options={["created_desc", "created_asc", "latency_desc", "seq_asc"].map((s) => ({ value: s, label: t(`adminLogs.conversation.sorts.${s}` as "adminLogs.conversation.sorts.created_desc") }))} />
        </>
      }
      detail={(r) => <ConversationDetail row={r} />}
    />
  );
}

function ConversationDetail({ row }: { row: ConversationEvent }) {
  const t = useT();
  const { locale } = useI18n();
  const [full, setFull] = useState<ConversationEvent>(row);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);

  // The list row shows at once; the detail fills in the large payload fields. Only the latest request may update it.
  useEffect(() => {
    const controller = new AbortController();
    setFull(row);
    setLoading(true);
    setFailed(false);
    getConversationEvent(row.id, controller.signal).then(
      (detail) => {
        setFull({ ...row, ...detail });
        setLoading(false);
      },
      (error: unknown) => {
        if (isAbortError(error)) return;
        setLoading(false);
        setFailed(true);
      },
    );
    return () => controller.abort();
  }, [row]);

  const blob = (title: string, text: string, omitted: boolean, size: number) =>
    text || omitted ? (
      <DetailSection title={title}>
        {text ? <JsonView text={text} /> : <p className="text-sm text-muted-foreground">{loading ? t("adminLogs.conversation.loadingPayload") : t("adminLogs.conversation.omitted", { size: formatBytes(size, locale) })}</p>}
      </DetailSection>
    ) : null;

  return (
    <div className="space-y-5">
      {failed ? (
        <p role="alert" className="text-sm font-medium text-destructive">
          {t("adminLogs.detailFailed")}
        </p>
      ) : null}
      <DetailList
        items={[
          [t("adminLogs.conversation.columns.time"), formatDateTime(full.startedAt, locale)],
          [t("adminLogs.conversation.columns.user"), `${userText(full)} (#${full.userID})`],
          [t("adminLogs.fields.conversation"), `#${full.conversationID}`],
          [t("adminLogs.conversation.run"), full.runID],
          [t("adminLogs.conversation.scope"), `${full.eventScope} / ${full.phase} / ${full.stage}`],
          [t("adminLogs.conversation.columns.type"), full.eventType],
          [t("adminLogs.conversation.columns.status"), full.status],
          [t("adminLogs.conversation.tool"), full.toolName],
          [t("adminLogs.conversation.model"), [full.platformModelName, full.upstreamName, full.upstreamModelName].filter(Boolean).join(" / ")],
          [t("adminLogs.conversation.columns.latency"), formatLatency(full.latencyMS, locale)],
          [t("adminLogs.conversation.seq"), full.seq],
        ]}
      />
      {full.contentMarkdown ? (
        <DetailSection title={t("adminLogs.conversation.content")}>
          <pre className="max-h-64 overflow-auto whitespace-pre-wrap rounded-lg border bg-muted/40 p-3 text-[13px]">{full.contentMarkdown}</pre>
        </DetailSection>
      ) : null}
      {blob(t("adminLogs.conversation.input"), full.inputJSON, full.inputOmitted, full.inputSizeBytes)}
      {blob(t("adminLogs.conversation.output"), full.outputJSON, full.outputOmitted, full.outputSizeBytes)}
      {blob(t("adminLogs.conversation.payload"), full.payloadJSON, full.payloadOmitted, full.payloadSizeBytes)}
      {blob(t("adminLogs.conversation.error"), full.errorJSON, full.errorOmitted, full.errorSizeBytes)}
    </div>
  );
}

// --- error logs ----------------------------------------------------------------------------------------

export function ErrorsTab({ remountKey }: TabProps) {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const [q, setQ] = useState("");
  const [statusClass, setStatusClass] = useState("");
  const filters = useMemo(() => ({ q, statusClass, remountKey }), [q, statusClass, remountKey]);
  const list = usePagedQuery({
    filters,
    fetch: ({ page, pageSize, filters: f, signal }) => listErrors({ page, page_size: pageSize, query: f.q.trim(), status_class: f.statusClass }, signal),
    onError: (error) => notify.error(loadFailed("errors"), error),
  });
  const copy = async (text: string) => {
    if (await copyText(text)) toast.success(t("adminCommon.toast.copied"));
    else toast.error(t("adminCommon.toast.copyFailed"));
  };
  const columns: Column<ErrorLog>[] = [
    { id: "time", header: t("adminLogs.errors.columns.time"), cell: (r) => time(r.createdAt, locale) },
    { id: "status", header: t("adminLogs.errors.columns.status"), cell: (r) => <Badge tone={r.statusCode >= 500 ? "danger" : "warning"}>{r.statusCode}</Badge> },
    { id: "request", header: t("adminLogs.errors.columns.request"), cell: (r) => <Mono className="break-all">{r.method} {r.route || r.path}</Mono> },
    { id: "code", header: t("adminLogs.errors.columns.code"), cell: (r) => <Mono>{r.errorCode || "-"}</Mono> },
    {
      id: "cause",
      header: t("adminLogs.errors.columns.cause"),
      className: "max-w-[20rem]",
      cell: (r) => (
        <span className="flex items-start gap-1">
          <span className="line-clamp-2 text-[13px] text-muted-foreground">{r.cause || "-"}</span>
          {r.cause ? (
            <Button
              variant="ghost"
              size="icon-xs"
              aria-label={t("adminLogs.errors.copyCause")}
              onClick={(event) => {
                event.stopPropagation();
                void copy(r.cause);
              }}
            >
              <Copy />
            </Button>
          ) : null}
        </span>
      ),
    },
  ];
  return (
    <LogView
      list={list}
      columns={columns}
      rowKey={(r) => r.id}
      label={t("adminLogs.tabs.errors")}
      empty={t("adminLogs.empty")}
      detailTitle={(r) => `${r.statusCode} ${r.errorCode}`.trim()}
      filters={
        <>
          <SearchBox value={q} onChange={setQ} placeholder={t("adminLogs.errors.search")} />
          <OptionSelect value={statusClass} onChange={setStatusClass} label={t("adminLogs.errors.columns.status")} options={[{ value: "", label: t("adminLogs.errors.anyClass") }, { value: "5xx", label: t("adminLogs.errors.class5xx") }, { value: "stream", label: t("adminLogs.errors.classStream") }]} />
        </>
      }
      detail={(r) => <ErrorDetail row={r} />}
    />
  );
}

function ErrorDetail({ row }: { row: ErrorLog }) {
  const t = useT();
  const { locale } = useI18n();
  const [raw, setRaw] = useState(false);
  return (
    <div className="space-y-5">
      <DetailSection title={t("adminLogs.errors.request")}>
        <DetailList
          items={[
            [t("adminLogs.errors.columns.time"), formatDateTime(row.createdAt, locale)],
            [t("adminLogs.errors.columns.status"), row.statusCode],
            [t("adminLogs.errors.columns.code"), row.errorCode],
            [t("adminLogs.errors.route"), `${row.method} ${row.route || row.path}`],
            [t("adminLogs.errors.path"), row.path],
            [t("adminLogs.fields.requestId"), row.requestID],
            [t("adminLogs.errors.trace"), row.traceID],
            [t("adminLogs.usage.columns.latency"), formatLatency(row.latencyMS, locale)],
          ]}
        />
      </DetailSection>
      <DetailSection title={t("adminLogs.errors.client")}>
        <DetailList items={[[t("adminLogs.fields.ip"), row.clientIP], [t("adminLogs.fields.userAgent"), row.userAgent]]} />
      </DetailSection>
      <DetailSection title={t("adminLogs.errors.user")}>
        <DetailList items={[[t("adminLogs.errors.account"), row.userID ? `${userText(row)} (#${row.userID})` : ""]]} />
      </DetailSection>
      {row.cause ? (
        <DetailSection title={t("adminLogs.errors.columns.cause")}>
          <p className="whitespace-pre-wrap break-words rounded-lg border bg-muted/40 p-3 text-[13px]">{row.cause}</p>
        </DetailSection>
      ) : null}
      {row.detail ? (
        <DetailSection title={t("adminLogs.fields.detail")}>
          <div className="mb-2 inline-flex rounded-md bg-muted p-0.5 text-xs">
            {[false, true].map((value) => (
              <button key={String(value)} type="button" onClick={() => setRaw(value)} className={`rounded px-2 py-1 ${raw === value ? "bg-background shadow-sm" : "text-muted-foreground"}`}>
                {value ? t("adminLogs.errors.rawView") : t("adminLogs.errors.formattedView")}
              </button>
            ))}
          </div>
          <JsonView text={row.detail} pretty={!raw} />
        </DetailSection>
      ) : null}
    </div>
  );
}
