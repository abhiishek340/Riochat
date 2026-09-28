"use client";

import { useMemo, useState } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useI18n, useT } from "@/lib/i18n/provider";
import { SettingsSection, StatTile } from "../parts";
import { formatInt, formatLatency } from "../lib/format";
import { BarChart, Legend, seriesColor, type BarDatum, type BarSeries } from "./bar-chart";
import { formatCost, type Display } from "./money";
import type { DaySummary, MonthSummary } from "./types";
import { dayLatencyFor, dayTokensBySeries, hasAnyUsage, modelSeries, monthSorted, OTHER_SERIES, seriesKeyOf, sumMetrics, visibleDays } from "./usage-logic";

type View = "daily" | "monthly";

export function UsageTrend({
  daily,
  monthly,
  loading,
  display,
  view,
  onView,
}: {
  daily: DaySummary[];
  monthly: MonthSummary[];
  loading: boolean;
  display: Display;
  view: View;
  onView: (view: View) => void;
}) {
  const t = useT();
  const { locale } = useI18n();
  const [hidden, setHidden] = useState<Set<string>>(new Set());

  const days = useMemo(() => visibleDays(daily), [daily]);
  const months = useMemo(() => monthSorted(monthly), [monthly]);
  const entries = view === "daily" ? days : months;
  const totals = sumMetrics(entries);
  const empty = !hasAnyUsage(entries);

  const models = useMemo(() => modelSeries(days), [days]);
  const dailySeries: BarSeries[] = useMemo(
    () =>
      models.length === 0
        ? [
            {
              key: "tokens",
              label: t("settings.billing.trend.tokensSeries"),
              color: seriesColor(0),
            },
          ]
        : models.map((m, i) => ({
            key: m.key,
            label: m.key === OTHER_SERIES ? t("settings.billing.trend.otherModels") : m.label,
            color: m.key === OTHER_SERIES ? "var(--muted-foreground)" : seriesColor(i),
          })),
    [models, t],
  );
  const monthSeries: BarSeries[] = useMemo(
    () => [
      {
        key: "tokens",
        label: t("settings.billing.trend.tokensSeries"),
        color: seriesColor(0),
      },
    ],
    [t],
  );

  const dailyData: BarDatum[] = useMemo(
    () =>
      days.map((d) => {
        const [, m = "", day = ""] = d.usageDate.slice(0, 10).split("-");
        const values: Record<string, number> = {};
        if (models.length === 0) values.tokens = d.totalTokens;
        else for (const [k, v] of dayTokensBySeries(d, models)) values[k] = v;
        return {
          id: d.usageDate,
          label: `${Number(m)}/${Number(day)}`,
          values,
        };
      }),
    [days, models],
  );
  const monthlyData: BarDatum[] = useMemo(
    () =>
      months.map((m) => {
        const date = new Date(m.monthStartAt);
        const label = Number.isNaN(date.getTime()) ? m.monthStartAt.slice(0, 7) : new Intl.DateTimeFormat(locale, { month: "short" }).format(date);
        return { id: m.monthStartAt, label, values: { tokens: m.totalTokens } };
      }),
    [months, locale],
  );

  const toggle = (key: string) =>
    setHidden((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key);
      else next.add(key);
      return next;
    });

  const series = view === "daily" ? dailySeries : monthSeries;

  const dailyTooltip = (datum: BarDatum, index: number) => {
    const day = days[index];
    if (!day) return null;
    const visibleModels = day.models.filter((m) => !hidden.has(seriesKeyOf(m.platformModelName, models)));
    const tokens = models.length === 0 ? day.totalTokens : visibleModels.reduce((s, m) => s + m.totalTokens, 0);
    return (
      <div className="grid gap-1.5">
        <div className="font-medium">{day.usageDate.slice(0, 10)}</div>
        <Row label={t("settings.billing.trend.tokens")} value={formatInt(tokens)} />
        <Row label={t("settings.billing.trend.cost")} value={formatCost(day.billedUSD, display, locale)} />
        <Row label={t("settings.billing.trend.calls")} value={formatInt(day.callCount)} />
        <Row label={t("settings.billing.trend.latency")} value={formatLatency(dayLatencyFor(day, models, hidden) || day.avgLatencyMS)} />
        {visibleModels.length > 0 ? (
          <div className="mt-1 grid gap-1 border-t pt-1.5">
            {[...visibleModels]
              .sort((a, b) => b.totalTokens - a.totalTokens)
              .slice(0, 10)
              .map((m) => (
                <Row key={m.platformModelName} label={m.platformModelName} value={formatInt(m.totalTokens)} mono />
              ))}
          </div>
        ) : null}
        <span className="sr-only">{datum.label}</span>
      </div>
    );
  };

  const monthlyTooltip = (_datum: BarDatum, index: number) => {
    const month = months[index];
    if (!month) return null;
    const date = new Date(month.monthStartAt);
    const title = Number.isNaN(date.getTime())
      ? month.monthStartAt.slice(0, 7)
      : new Intl.DateTimeFormat(locale, {
          year: "numeric",
          month: "long",
        }).format(date);
    return (
      <div className="grid gap-1.5">
        <div className="font-medium">{title}</div>
        <Row label={t("settings.billing.trend.tokens")} value={formatInt(month.totalTokens)} />
        <Row label={t("settings.billing.trend.cost")} value={formatCost(month.billedUSD, display, locale)} />
        <Row label={t("settings.billing.trend.calls")} value={formatInt(month.callCount)} />
        <Row label={t("settings.billing.trend.latency")} value={formatLatency(month.avgLatencyMS)} />
      </div>
    );
  };

  return (
    // The switch and the panel it controls share one tabs root.
    <Tabs value={view} onValueChange={(v) => onView(v as View)}>
      <SettingsSection
        title={t("settings.billing.trend.title")}
        bare
        actions={
          <TabsList>
            <TabsTrigger value="daily">{t("settings.billing.trend.daily")}</TabsTrigger>
            <TabsTrigger value="monthly">{t("settings.billing.trend.monthly")}</TabsTrigger>
          </TabsList>
        }
      >
        <TabsContent value={view} className="mt-0 grid gap-4 rounded-xl border bg-card p-4 shadow-sm">
          <div className="grid grid-cols-2 gap-2.5 lg:grid-cols-4">
            <StatTile
              label={view === "daily" ? t("settings.billing.trend.costPeriod") : t("settings.billing.trend.costYear")}
              value={formatCost(totals.cost, display, locale)}
              loading={loading}
            />
            <StatTile label={t("settings.billing.trend.totalTokens")} value={formatInt(totals.tokens)} loading={loading} />
            <StatTile label={t("settings.billing.trend.totalCalls")} value={formatInt(totals.calls)} loading={loading} />
            <StatTile label={t("settings.billing.trend.avgLatency")} value={formatLatency(totals.latency)} loading={loading} />
          </div>
          {loading ? (
            <Skeleton className="h-64 w-full" />
          ) : empty ? (
            <p className="flex h-40 items-center justify-center text-sm text-muted-foreground">{t("settings.billing.trend.empty")}</p>
          ) : (
            <>
              <Legend series={series} hidden={hidden} onToggle={toggle} />
              <BarChart
                data={view === "daily" ? dailyData : monthlyData}
                series={series}
                hidden={hidden}
                label={t("settings.billing.trend.chartLabel")}
                tooltip={view === "daily" ? dailyTooltip : monthlyTooltip}
                tickEvery={view === "daily" ? Math.max(1, Math.ceil(dailyData.length / 10)) : 1}
              />
            </>
          )}
        </TabsContent>
      </SettingsSection>
    </Tabs>
  );
}

function Row({ label, value, mono }: { label: string; value: string; mono?: boolean }) {
  return (
    <div className="flex items-baseline justify-between gap-3">
      <span className={mono ? "truncate font-mono text-muted-foreground" : "text-muted-foreground"}>{label}</span>
      <span className="shrink-0 tabular-nums">{value}</span>
    </div>
  );
}
