"use client";

import { Filter, RefreshCw } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { api } from "@/lib/api/client";
import { isAbortError } from "@/lib/api/errors";
import { useCapabilities } from "@/lib/capabilities/provider";
import { useI18n, useT } from "@/lib/i18n/provider";
import { BarList, LineChart } from "../components/charts";
import { ModelIcon } from "../components/model-icon";
import { AdminPage, Section } from "../components/page";
import { AdminSection } from "../components/section-guard";
import { StatTile } from "../components/stat-tile";
import { OptionSelect } from "../components/toolbar";
import { endOfDayIso, formatDisplayMoney, formatLatency, formatNumber, startOfDayIso } from "../lib/format";
import { readPage } from "../lib/paging";
import { loadAllModels, loadBillingConfig, loadPermissionGroups } from "../lib/reference";
import { useDebouncedValue } from "@/hooks/use-debounced-value";
import { useNotify } from "../lib/use-notify";
import { DEFAULT_BILLING_CONFIG, type BillingConfig, type PermissionGroup, type PlatformModel, type UserRecord } from "../lib/wire";
import { ModerationStatsPanel } from "../moderation";
import { loadStats } from "./api";
import { activeFilterCount, bucketLabel, buildQuery, cacheKey, metricValue, METRICS, NO_FILTERS, presetRange, PRESETS, RankCache, validateRange, withSubject, type Filters, type Metric, type Stats } from "./rules";

const DEFAULT_DAYS = 30;

export function AdminStatisticsPage() {
  return (
    <AdminSection>
      <StatisticsContent />
    </AdminSection>
  );
}

function StatisticsContent() {
  const t = useT();
  const { locale } = useI18n();
  const notify = useNotify();
  const { flags } = useCapabilities();
  const initial = useMemo(() => presetRange(DEFAULT_DAYS), []);
  const [preset, setPreset] = useState<number | "custom">(DEFAULT_DAYS);
  const [start, setStart] = useState(initial.start);
  const [end, setEnd] = useState(initial.end);
  const [filters, setFilters] = useState<Filters>(NO_FILTERS);
  const [panel, setPanel] = useState(false);
  const [modelRank, setModelRank] = useState<Metric>("cost");
  const [userRank, setUserRank] = useState<Metric>("cost");
  const [stats, setStats] = useState<Stats | null>(null);
  const [loading, setLoading] = useState(false);
  const [trendMetric, setTrendMetric] = useState<Metric>("cost");
  const [config, setConfig] = useState<BillingConfig>(DEFAULT_BILLING_CONFIG);
  const [models, setModels] = useState<PlatformModel[]>([]);
  const [groups, setGroups] = useState<PermissionGroup[]>([]);
  const [nonce, setNonce] = useState(0);
  const modelCache = useRef(new RankCache<Stats["topModels"]>());
  const userCache = useRef(new RankCache<Stats["topUsers"]>());
  // A full load and each ranking refresh count their own requests, so a ranking
  // change made while the page is loading cannot cancel the full answer.
  const fullRequest = useRef(0);
  const rankRequest = useRef({ models: 0, users: 0 });
  const latestRank = useRef<{ model: Metric; user: Metric }>({ model: "cost", user: "cost" });
  const fullInFlight = useRef(false);
  const changeRankRef = useRef<(kind: "models" | "users", next: Metric) => Promise<void>>(async () => undefined);
  const applied = useRef<{ model: Metric; user: Metric }>({ model: "cost", user: "cost" });

  // Reference data loads once; one toast covers a failure of any of it.
  useEffect(() => {
    Promise.all([loadBillingConfig(), loadAllModels(), flags.multiUser ? loadPermissionGroups() : Promise.resolve([])]).then(
      ([c, m, g]) => {
        setConfig(c);
        setModels(m);
        setGroups(g);
      },
      (error: unknown) => notify.error("adminStats.toast.referenceFailed", error),
    );
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [flags.multiUser]);

  const problem = validateRange(start, end);
  const key = cacheKey(start, end, filters);

  const fetchStats = useCallback(
    async (section: "all" | "models" | "users", modelBy: Metric, userBy: Metric, signal: AbortSignal) => {
      const query = buildQuery({ start, end, filters, section, modelRankBy: modelBy, userRankBy: userBy });
      return loadStats(query, signal);
    },
    [start, end, filters],
  );

  // A new range, filter or refresh loads everything; only the newest answer counts.
  useEffect(() => {
    if (problem) {
      fullRequest.current += 1;
      fullInFlight.current = false;
      setLoading(false);
      return;
    }
    const mine = ++fullRequest.current;
    const asked = { ...latestRank.current };
    rankRequest.current = { models: rankRequest.current.models + 1, users: rankRequest.current.users + 1 };
    const controller = new AbortController();
    modelCache.current.reset(key);
    userCache.current.reset(key);
    setLoading(true);
    fullInFlight.current = true;
    fetchStats("all", asked.model, asked.user, controller.signal).then(
      (result) => {
        if (mine !== fullRequest.current) return;
        modelCache.current.set(asked.model, result.topModels);
        userCache.current.set(asked.user, result.topUsers);
        applied.current = { model: asked.model, user: asked.user };
        setStats(result);
        setLoading(false);
        fullInFlight.current = false;
        // A ranking picked while this answer was on its way is fetched now.
        if (latestRank.current.model !== asked.model) void changeRankRef.current("models", latestRank.current.model);
        if (latestRank.current.user !== asked.user) void changeRankRef.current("users", latestRank.current.user);
      },
      (error: unknown) => {
        if (mine !== fullRequest.current || isAbortError(error)) return;
        setLoading(false);
        fullInFlight.current = false;
        notify.error("adminStats.toast.loadFailed", error);
      },
    );
    return () => controller.abort();
    // The rank metrics are handled separately below.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [key, nonce, problem]);

  /** Changing one rank metric refreshes only its section; answers are cached per metric until a filter changes. */
  const changeRank = async (kind: "models" | "users", next: Metric) => {
    const cache = kind === "models" ? modelCache.current : userCache.current;
    const set = kind === "models" ? setModelRank : setUserRank;
    const slot = kind === "models" ? "model" : "user";
    const before = applied.current[slot];
    set(next);
    latestRank.current[slot] = next;
    const hit = cache.get(next);
    if (hit) {
      setStats((s) => (s ? (kind === "models" ? { ...s, topModels: hit as Stats["topModels"] } : { ...s, topUsers: hit as Stats["topUsers"] }) : s));
      applied.current[kind === "models" ? "model" : "user"] = next;
      return;
    }
    // While the full answer is still loading it will fetch this ranking itself.
    if (problem || fullInFlight.current) return;
    const mine = ++rankRequest.current[kind];
    const fullAtStart = fullRequest.current;
    try {
      const result = await fetchStats(kind, kind === "models" ? next : latestRank.current.model, kind === "users" ? next : latestRank.current.user, new AbortController().signal);
      if (mine !== rankRequest.current[kind] || fullAtStart !== fullRequest.current) return;
      if (kind === "models") {
        modelCache.current.set(next, result.topModels);
        setStats((s) => (s ? { ...s, topModels: result.topModels } : s));
      } else {
        userCache.current.set(next, result.topUsers);
        setStats((s) => (s ? { ...s, topUsers: result.topUsers } : s));
      }
      applied.current[kind === "models" ? "model" : "user"] = next;
    } catch (error) {
      if (mine !== rankRequest.current[kind] || fullAtStart !== fullRequest.current) return;
      set(before);
      latestRank.current[slot] = before;
      notify.error("adminStats.toast.rankFailed", error);
    }
  };
  changeRankRef.current = changeRank;

  const money = useCallback((usd: number, digits = 4) => formatDisplayMoney(usd, { currency: config.displayCurrency, rate: config.usdToCNYRate }, locale, digits), [config, locale]);
  const fmt = useCallback((metric: Metric) => (value: number) => (metric === "cost" ? money(value, 2) : formatNumber(value, locale)), [money, locale]);
  const t0 = stats?.totals;
  const range = stats?.range ?? { startDate: start, endDate: end, granularity: "day" };
  const points = (stats?.trend ?? []).map((p) => ({ label: bucketLabel(p.periodStart, range.granularity, range, locale), value: metricValue(p, trendMetric) }));
  const rangeStartIso = startOfDayIso(start);
  const rangeEndIso = endOfDayIso(end);

  const choosePreset = (days: number) => {
    const r = presetRange(days);
    setPreset(days);
    setStart(r.start);
    setEnd(r.end);
  };
  const metricLabel = (m: Metric) => t(`adminStats.metrics.${m}`);

  return (
    <AdminPage
      title={t("adminStats.title")}
      description={t("adminStats.description")}
      actions={
        <>
          <Button variant="outline" onClick={() => setPanel((p) => !p)} aria-expanded={panel}>
            <Filter />
            {t("adminStats.filters.title")}
            {activeFilterCount(filters) > 0 ? <Badge tone="primary">{activeFilterCount(filters)}</Badge> : null}
          </Button>
          <Button variant="outline" size="icon" aria-label={t("common.actions.refresh")} onClick={() => setNonce((n) => n + 1)} loading={loading}>
            <RefreshCw />
          </Button>
        </>
      }
    >
      <Card className="space-y-3 p-4">
        <div className="flex flex-wrap items-end gap-3">
          <div role="group" aria-label={t("adminStats.range.title")} className="inline-flex rounded-lg bg-muted p-1">
            {PRESETS.map((days) => (
              <button key={days} type="button" aria-pressed={preset === days} onClick={() => choosePreset(days)} className={`rounded-md px-3 py-1 text-sm font-medium ${preset === days ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground"}`}>
                {t("adminStats.range.lastDays", { days })}
              </button>
            ))}
            <button type="button" aria-pressed={preset === "custom"} onClick={() => setPreset("custom")} className={`rounded-md px-3 py-1 text-sm font-medium ${preset === "custom" ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground"}`}>
              {t("adminStats.range.custom")}
            </button>
          </div>
          <div className="flex items-center gap-1.5">
            <Input
              type="date"
              value={start}
              aria-label={t("adminCommon.dates.startDate")}
              aria-invalid={problem !== null || undefined}
              onChange={(e) => {
                setStart(e.target.value);
                setPreset("custom");
              }}
              className="h-9 w-40"
            />
            <span aria-hidden className="text-muted-foreground">
              –
            </span>
            <Input
              type="date"
              value={end}
              aria-label={t("adminCommon.dates.endDate")}
              aria-invalid={problem !== null || undefined}
              onChange={(e) => {
                setEnd(e.target.value);
                setPreset("custom");
              }}
              className="h-9 w-40"
            />
          </div>
        </div>
        {problem ? (
          <p role="alert" className="text-sm font-medium text-destructive">
            {t(`adminStats.range.problems.${problem}`)}
          </p>
        ) : null}
        {panel ? (
          <FilterPanel filters={filters} models={models} groups={groups} multiUser={flags.multiUser} onChange={setFilters} />
        ) : null}
      </Card>

      <div className="grid gap-3 sm:grid-cols-2 lg:grid-cols-4">
        {loading && !stats ? (
          Array.from({ length: 4 }, (_, i) => <Skeleton key={i} className="h-24" />)
        ) : (
          <>
            <StatTile label={t("adminStats.tiles.cost")} value={money(t0?.billedUSD ?? 0)} hint={config.displayCurrency === "CNY" ? t("adminStats.tiles.rate", { rate: config.usdToCNYRate }) : undefined} />
            <StatTile label={t("adminStats.tiles.tokens")} value={formatNumber(t0?.totalTokens ?? 0, locale)} />
            <StatTile label={t("adminStats.tiles.calls")} value={formatNumber(t0?.callCount ?? 0, locale)} />
            <StatTile label={t("adminStats.tiles.latency")} value={t0 && t0.recordCount > 0 ? formatLatency(t0.avgLatencyMS, locale) : "-"} />
          </>
        )}
      </div>

      <Section
        title={t("adminStats.trend.title")}
        actions={<MetricSwitch value={trendMetric} onChange={setTrendMetric} label={t("adminStats.trend.metric")} metricLabel={metricLabel} />}
      >
        {loading && !stats ? <Skeleton className="h-56 w-full" /> : <LineChart points={points} format={fmt(trendMetric)} label={t("adminStats.trend.title")} empty={t("adminStats.empty")} />}
      </Section>

      <div className="grid gap-4 lg:grid-cols-2">
        <Section title={t("adminStats.models.title")} actions={<MetricSwitch value={modelRank} onChange={(m) => void changeRank("models", m)} label={t("adminStats.rank")} metricLabel={metricLabel} />}>
          <BarList
            items={(stats?.topModels ?? []).map((m) => ({
              key: m.platformModelName,
              label: (
                <span className="flex items-center gap-2">
                  <ModelIcon icon={models.find((x) => x.platformModelName === m.platformModelName)?.icon} name={m.platformModelName} size={18} />
                  {m.platformModelName}
                </span>
              ),
              value: metricValue(m, modelRank),
            }))}
            format={fmt(modelRank)}
            empty={t("adminStats.empty")}
          />
        </Section>
        <Section title={t("adminStats.users.title")} actions={<MetricSwitch value={userRank} onChange={(m) => void changeRank("users", m)} label={t("adminStats.rank")} metricLabel={metricLabel} />}>
          <BarList
            items={(stats?.topUsers ?? []).map((u) => ({ key: u.userID, label: u.userLabel || u.userDisplayName || u.username || `#${u.userID}`, value: metricValue(u, userRank) }))}
            format={fmt(userRank)}
            empty={t("adminStats.empty")}
          />
        </Section>
      </div>

      {flags.contentModeration && !problem ? <ModerationStatsPanel from={rangeStartIso} to={rangeEndIso} /> : null}
    </AdminPage>
  );
}
