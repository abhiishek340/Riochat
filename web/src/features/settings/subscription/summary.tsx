"use client";

import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { Tooltip } from "@/components/ui/tooltip";
import { useI18n, useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { formatDate } from "../lib/format";
import { SettingRow, SettingsSection, StatTile } from "../parts";
import { formatMoney, priceAmountText, normalizeInterval } from "./money";
import { defaultPrice, resolveCurrentPlan, sortEntitlements, usagePercent } from "./plans-logic";
import type { BillingContext } from "./payment-dialogs";

export function BillingSummary({
  ctx,
  topupVisible,
  onRedeem,
  onTopup,
  onPlans,
}: {
  ctx: BillingContext;
  topupVisible: boolean;
  onRedeem: () => void;
  onTopup: () => void;
  onPlans: () => void;
}) {
  const t = useT();
  const { locale } = useI18n();
  const mode = ctx.config?.mode ?? "self";
  const { overview, display, loading } = ctx;

  if (loading && !ctx.config) {
    return (
      <SettingsSection title={t("settings.billing.summary.title")} bare>
        <div className="grid gap-3 rounded-xl border bg-card p-5">
          <Skeleton className="h-5 w-48" />
          <Skeleton className="h-16 w-full" />
        </div>
      </SettingsSection>
    );
  }

  if (mode === "self") {
    return (
      <SettingsSection title={t("settings.billing.summary.title")}>
        <SettingRow title={t("settings.billing.summary.selfTitle")} description={t("settings.billing.summary.selfBody")} />
      </SettingsSection>
    );
  }

  if (mode === "usage") {
    return (
      <SettingsSection title={t("settings.billing.summary.title")} bare>
        <div className="grid gap-4 rounded-xl border bg-card p-4 shadow-sm">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="grid gap-0.5">
              <h4 className="text-sm font-medium">{t("settings.billing.summary.usageTitle")}</h4>
              <p className="text-[13px] text-muted-foreground">{t("settings.billing.summary.usageBody")}</p>
            </div>
            <div className="flex gap-2">
              <Button variant="outline" size="sm" disabled={loading} onClick={onRedeem}>
                {t("settings.billing.summary.redeem")}
              </Button>
              {topupVisible ? (
                <Button size="sm" disabled={loading} onClick={onTopup}>
                  {t("settings.billing.summary.topup")}
                </Button>
              ) : null}
            </div>
          </div>
          <div className="grid grid-cols-2 gap-2.5">
            <StatTile label={t("settings.billing.summary.balance")} value={formatMoney(overview?.balanceUSD ?? 0, display, locale)} loading={loading} />
            <StatTile label={t("settings.billing.summary.spent")} value={formatMoney(overview?.totalSpentUSD ?? 0, display, locale)} loading={loading} />
          </div>
        </div>
      </SettingsSection>
    );
  }

  // period
  const user = ctx.user;
  const plan = resolveCurrentPlan(ctx.plans, overview, user);
  const price = plan ? defaultPrice(plan) : undefined;
  const credit = overview?.periodCreditUSD ?? plan?.periodCreditUSD ?? 0;
  const used = overview?.periodUsedUSD ?? 0;
  const percent = usagePercent(used, credit);
  const entitlements = sortEntitlements(overview?.entitlements ?? []);
  const range =
    overview?.periodStartAt && overview.periodEndAt
      ? `${formatDate(overview.periodStartAt, locale)} – ${formatDate(overview.periodEndAt, locale)}`
      : t("settings.billing.summary.periodGeneric");

  return (
    <SettingsSection title={t("settings.billing.summary.title")} bare>
      <div className="grid gap-4 rounded-xl border bg-card p-4 shadow-sm">
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div className="grid gap-0.5">
            <span className="text-xs text-muted-foreground">{t("settings.billing.summary.currentPlan")}</span>
            <h4 className="text-lg font-semibold">{plan ? plan.name || plan.code : t("settings.billing.summary.noPlan")}</h4>
            <p className="text-[13px] text-muted-foreground">
              {plan && price
                ? t("settings.billing.summary.planLine", {
                    price: priceAmountText(price, display, locale),
                    interval: t(`settings.billing.plans.intervals.${normalizeInterval(price.billingInterval)}`),
                    credit: formatMoney(credit, display, locale),
                  })
                : t("settings.billing.summary.planEmpty")}
            </p>
          </div>
          <div className="flex gap-2">
            <Button variant="outline" size="sm" disabled={loading} onClick={onRedeem}>
              {t("settings.billing.summary.redeem")}
            </Button>
            <Button size="sm" disabled={loading || ctx.plans.length === 0} onClick={onPlans}>
              {t("settings.billing.summary.subscribe")}
            </Button>
          </div>
        </div>

        {entitlements.length > 0 ? (
          <div className="grid gap-1.5">
            <span className="text-xs text-muted-foreground">{t("settings.billing.summary.queue", { count: entitlements.length })}</span>
            <ul className="flex flex-wrap gap-1.5">
              {entitlements.map((e) => {
                const p = e.plan;
                const tip = `${formatDate(e.currentPeriodStartAt || e.startAt, locale)} – ${e.currentPeriodEndAt ? formatDate(e.currentPeriodEndAt, locale) : "∞"} · ${formatMoney(p?.periodCreditUSD ?? 0, display, locale)}`;
                return (
                  <li key={e.id}>
                    <Tooltip label={tip}>
                      <span tabIndex={0} className="inline-flex cursor-default items-center rounded-full border bg-background px-2.5 py-1 text-xs font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring">{p ? p.name || p.code : `#${e.planId}`}</span>
                    </Tooltip>
                  </li>
                );
              })}
            </ul>
          </div>
        ) : null}

        <div className="grid gap-2 rounded-lg bg-muted/50 p-3.5">
          <div className="flex flex-wrap items-baseline justify-between gap-2 text-sm">
            <span className="font-medium">{t("settings.billing.summary.periodUsage")}</span>
            <span className="text-xs text-muted-foreground">{range}</span>
          </div>
          <div
            role="progressbar"
            aria-valuemin={0}
            aria-valuemax={100}
            aria-valuenow={Math.round(percent)}
            aria-label={t("settings.billing.summary.periodUsage")}
            className="h-2 overflow-hidden rounded-full bg-border"
          >
            <div className={cn("h-full rounded-full bg-primary transition-[width]", percent >= 90 && "bg-warning")} style={{ width: `${percent}%` }} />
          </div>
          <div className="flex flex-wrap justify-between gap-2 text-[13px] tabular-nums text-muted-foreground">
            <span>{t("settings.billing.summary.usedOf", { used: formatMoney(used, display, locale), credit: formatMoney(credit, display, locale) })}</span>
            <span>{Math.round(percent)}%</span>
          </div>
        </div>

        <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-background px-3.5 py-3">
          <div className="grid gap-0.5">
            <span className="text-sm font-medium">{t("settings.billing.summary.overage")}</span>
            <span className="text-[13px] text-muted-foreground">{t("settings.billing.summary.overageBody")}</span>
          </div>
          <div className="flex items-center gap-3">
            <span className="text-base font-semibold tabular-nums">{formatMoney(overview?.balanceUSD ?? 0, display, locale)}</span>
            {topupVisible ? (
              <Button size="sm" variant="outline" disabled={loading} onClick={onTopup}>
                {t("settings.billing.summary.topup")}
              </Button>
            ) : null}
          </div>
        </div>
      </div>
    </SettingsSection>
  );
}

