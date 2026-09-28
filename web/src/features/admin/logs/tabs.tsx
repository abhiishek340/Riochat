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
