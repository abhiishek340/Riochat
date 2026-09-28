"use client";

import { useMemo, useState } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useI18n, useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { SettingsSection, StatTile } from "../parts";
import { formatDate, formatInt } from "../lib/format";
import type { ActivityDay } from "./api";
import { buildWeeks, cellLevel, computeStreaks, hasAnyActivity, monthMarks, peakTokens, totalRequests, type Metric } from "./heatmap-logic";

const LEVEL_CLASS = ["bg-muted", "bg-primary/25", "bg-primary/45", "bg-primary/70", "bg-primary"] as const;
const CELL = 12;
const GAP = 3;

export function ActivityHeatmap({ days, loading }: { days: ActivityDay[]; loading: boolean }) {
  const t = useT();
  const { locale } = useI18n();
  const [metric, setMetric] = useState<Metric>("tokens");
  const [hover, setHover] = useState<string>("");

  const weeks = useMemo(() => buildWeeks(days), [days]);
  const marks = useMemo(() => monthMarks(weeks), [weeks]);
  const streaks = useMemo(() => computeStreaks(days), [days]);
  const peak = useMemo(() => peakTokens(days), [days]);
  const empty = !hasAnyActivity(days);

  const describe = (cell: { date: string; requestCount: number; tokenUsage: number }) =>
    t("settings.billing.heatmap.cell", {
      date: formatDate(`${cell.date}T00:00:00`, locale),
      requests: formatInt(cell.requestCount),
      tokens: formatInt(cell.tokenUsage),
    });

  return (
    // The switch and the panel it controls share one tabs root.
    <Tabs value={metric} onValueChange={(v) => setMetric(v as Metric)}>
      <SettingsSection
        title={t("settings.billing.heatmap.title")}
        bare
        actions={
          <TabsList>
            <TabsTrigger value="tokens">{t("settings.billing.heatmap.tokens")}</TabsTrigger>
            <TabsTrigger value="requests">{t("settings.billing.heatmap.requests")}</TabsTrigger>
          </TabsList>
        }
      >
        <TabsContent value={metric} className="mt-0 grid gap-4 rounded-xl border bg-card p-4 shadow-sm">
          <div className="grid grid-cols-2 gap-2.5 lg:grid-cols-4">
            <StatTile label={t("settings.billing.heatmap.peak")} value={formatInt(peak)} loading={loading} />
            <StatTile
              label={t("settings.billing.heatmap.current")}
              value={t("settings.billing.heatmap.days", {
                count: streaks.current,
              })}
              loading={loading}
            />
            <StatTile
              label={t("settings.billing.heatmap.longest")}
              value={t("settings.billing.heatmap.days", {
                count: streaks.longest,
              })}
              loading={loading}
            />
            <StatTile label={t("settings.billing.heatmap.active")} value={formatInt(streaks.activeDays)} loading={loading} />
          </div>
          {loading ? (
            <Skeleton className="h-28 w-full" />
          ) : empty ? (
            <p className="flex h-24 items-center justify-center text-sm text-muted-foreground">{t("settings.billing.heatmap.empty")}</p>
          ) : (
            <div className="grid gap-2">
              <div className="overflow-x-auto pb-1">
                <div className="relative w-max" style={{ paddingTop: 16 }}>
                  {marks.map((m) => (
                    <span key={`${m.year}-${m.month}`} className="absolute top-0 text-[11px] text-muted-foreground" style={{ left: m.column * (CELL + GAP) }}>
                      {new Intl.DateTimeFormat(locale, {
                        month: "short",
                      }).format(new Date(m.year, m.month, 1))}
                    </span>
                  ))}
                  <div
                    className="flex"
                    style={{ gap: GAP }}
                    role="img"
                    aria-label={`${t("settings.billing.heatmap.title")}. ${t("settings.billing.heatmap.totalRequests", { count: formatInt(totalRequests(days)) })}`}
                  >
                    {weeks.map((week, c) => (
                      <div key={c} className="grid" style={{ gap: GAP }}>
                        {week.map((cell, r) =>
                          cell ? (
                            <span
                              key={cell.date}
                              title={describe(cell)}
                              onMouseEnter={() => setHover(describe(cell))}
                              onMouseLeave={() => setHover("")}
                              className={cn("rounded-[3px]", LEVEL_CLASS[cellLevel(metric === "tokens" ? cell.tokenUsage : cell.requestCount, metric, peak)])}
                              style={{ width: CELL, height: CELL }}
                            />
                          ) : (
                            <span key={`b${r}`} style={{ width: CELL, height: CELL }} aria-hidden />
                          ),
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              </div>
              <div className="flex flex-wrap items-center justify-between gap-2 text-xs text-muted-foreground">
                <span className="min-h-4">
                  {hover ||
                    t("settings.billing.heatmap.totalRequests", {
                      count: formatInt(totalRequests(days)),
                    })}
                </span>
                <span className="flex items-center gap-1.5">
                  {t("settings.billing.heatmap.less")}
                  {LEVEL_CLASS.map((c) => (
                    <span key={c} className={cn("size-3 rounded-[3px]", c)} />
                  ))}
                  {t("settings.billing.heatmap.more")}
                </span>
              </div>
            </div>
          )}
        </TabsContent>
      </SettingsSection>
    </Tabs>
  );
}
