"use client";

import { useEffect, useMemo, useState } from "react";
import { isAbortError } from "@/lib/api/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import { LoadError } from "../components/load-error";
import { Section } from "../components/page";
import { StatTile } from "../components/stat-tile";
import { formatLatency, formatNumber } from "../lib/format";
import { useNotify } from "../lib/use-notify";
import { getStats } from "./api";
import { dailyStats, totalsOf, type StatItem } from "./rules";

/** Daily moderation counters for a period; used at the bottom of the statistics page. */
export function ModerationStatsPanel({ from, to }: { from?: string; to?: string }) {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const [items, setItems] = useState<StatItem[] | null>(null);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    const controller = new AbortController();
    setItems(null);
    setFailed(false);
    getStats({ from, to }, controller.signal).then(setItems, (error: unknown) => {
      if (isAbortError(error)) return;
      setItems([]);
      setFailed(true);
      notify.error("adminModeration.stats.loadFailed", error);
    });
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [from, to, attempt]);

  const totals = useMemo(() => totalsOf(items ?? []), [items]);
  const days = useMemo(() => dailyStats(items ?? []), [items]);
  const percent = (value: number) => `${new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(value * 100)}%`;

  return (
    <Section title={t("adminModeration.stats.title")} description={t("adminModeration.stats.description")}>
      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-5">
        <StatTile label={t("adminModeration.stats.checks")} value={formatNumber(totals.checks, locale)} />
        <StatTile label={t("adminModeration.stats.hits")} value={formatNumber(totals.hits, locale)} />
        <StatTile label={t("adminModeration.stats.hitRate")} value={percent(totals.hitRate)} />
        <StatTile label={t("adminModeration.stats.failures")} value={formatNumber(totals.failures, locale)} />
        <StatTile label={t("adminModeration.stats.latency")} value={totals.avgLatencyMs === null ? "-" : formatLatency(totals.avgLatencyMs, locale)} />
      </div>
      {failed ? <LoadError className="mt-4" message={t("adminModeration.stats.loadFailed")} onRetry={() => setAttempt((n) => n + 1)} /> : null}
      {!failed && items !== null && days.length === 0 ? <p className="mt-4 text-sm text-muted-foreground">{t("adminModeration.stats.empty")}</p> : null}
      {days.length > 0 ? (
        <div className="mt-4 overflow-auto rounded-lg border">
          <table className="w-full text-sm">
            <thead className="bg-muted/60 text-left text-xs uppercase tracking-wide text-muted-foreground">
              <tr>
                <th className="px-3 py-2">{t("adminModeration.stats.day")}</th>
                <th className="px-3 py-2 text-right">{t("adminModeration.stats.checks")}</th>
                <th className="px-3 py-2 text-right">{t("adminModeration.stats.hits")}</th>
                <th className="px-3 py-2 text-right">{t("adminModeration.stats.hitRate")}</th>
                <th className="px-3 py-2 text-right">{t("adminModeration.stats.failures")}</th>
                <th className="px-3 py-2 text-right">{t("adminModeration.stats.latency")}</th>
              </tr>
            </thead>
            <tbody className="tabular-nums">
              {[...days].reverse().map((d) => (
                <tr key={d.date} className="border-t">
                  <td className="px-3 py-2">{d.date}</td>
                  <td className="px-3 py-2 text-right">{formatNumber(d.checks, locale)}</td>
                  <td className="px-3 py-2 text-right">{formatNumber(d.hits, locale)}</td>
                  <td className="px-3 py-2 text-right">{percent(d.hitRate)}</td>
                  <td className="px-3 py-2 text-right">{formatNumber(d.failures, locale)}</td>
                  <td className="px-3 py-2 text-right">{d.avgLatencyMs === null ? "-" : formatLatency(d.avgLatencyMs, locale)}</td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      ) : null}
    </Section>
  );
}
