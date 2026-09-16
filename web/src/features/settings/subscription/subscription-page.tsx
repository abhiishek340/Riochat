"use client";

import { useCallback, useEffect, useMemo, useState } from "react";
import { usePageTitle } from "@/components/shell/header-context";
import { toast } from "@/components/ui/toast-store";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT } from "@/lib/i18n/provider";
import { useSession } from "@/lib/session/provider";
import { LoadErrorNotice, SettingsPage } from "../parts";
import { useLoad } from "../lib/use-load";
import { fetchActivity, fetchConfig, fetchDaily, fetchMonthly, fetchOverview, fetchPlans } from "./api";
import { ActivityHeatmap } from "./heatmap";
import { displayOf } from "./money";
import { PaymentDialog, PlansDialog, RedeemDialog, TopupDialog, type BillingContext, type PaymentTarget } from "./payment-dialogs";
import { paymentEnabled } from "./payment-logic";
import { BillingSummary } from "./summary";
import type { BillingConfig, DaySummary, MonthSummary, Overview, Plan } from "./types";
import { UsageLog } from "./usage-log";
import { UsageTrend } from "./usage-trend";

interface Bundle {
  config: BillingConfig | null;
  plans: Plan[];
  overview: Overview | null;
  daily: DaySummary[];
  monthly: MonthSummary[];
  firstError: unknown;
}

async function loadBundle(signal: AbortSignal): Promise<Bundle> {
  const [config, plans, overview, daily, monthly] = await Promise.allSettled([
    fetchConfig(signal),
    fetchPlans(signal),
    fetchOverview(signal),
    fetchDaily(signal),
    fetchMonthly(signal),
  ]);
  const failed = [config, plans, overview, daily, monthly].find((r) => r.status === "rejected");
  return {
    config: config.status === "fulfilled" ? config.value : null,
    plans: plans.status === "fulfilled" ? plans.value : [],
    overview: overview.status === "fulfilled" ? overview.value : null,
    daily: daily.status === "fulfilled" ? daily.value : [],
    monthly: monthly.status === "fulfilled" ? monthly.value : [],
    firstError: failed && failed.status === "rejected" ? failed.reason : undefined,
  };
}

export function SubscriptionSettingsPage() {
  const t = useT();
  const errorMessage = useErrorMessage();
  usePageTitle(t("settings.title"));
  const { user, accessToken, reloadUser } = useSession();
  const enabled = Boolean(accessToken && user);
  const bundle = useLoad(loadBundle, `${accessToken}|${user?.publicID ?? ""}`, enabled);
  const activity = useLoad(
    async (signal) => {
      try {
        return await fetchActivity(signal);
      } catch (e) {
        if (signal.aborted) throw e;
        toast.error(t("settings.billing.heatmap.loadFailed"), { description: errorMessage(e) });
        return [];
      }
    },
    `${accessToken}`,
    enabled,
  );

  const [overviewOverride, setOverviewOverride] = useState<Overview | null>(null);
  const [view, setView] = useState<"daily" | "monthly">("daily");
  const [plansOpen, setPlansOpen] = useState(false);
  const [topupOpen, setTopupOpen] = useState(false);
  const [redeemOpen, setRedeemOpen] = useState(false);
  const [payment, setPayment] = useState<PaymentTarget | null>(null);
  const [logKey, setLogKey] = useState(0);

  const data = bundle.data;
  useEffect(() => {
    if (data) setOverviewOverride(null);
  }, [data]);
  useEffect(() => {
    if (data?.firstError) toast.error(t("settings.billing.loadFailed"), { description: errorMessage(data.firstError) });
    // One toast per load.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [data]);

  // Back from the payment provider.
  useEffect(() => {
    const params = new URLSearchParams(window.location.search);
    const outcome = params.get("payment");
    if (!outcome) return;
    if (outcome === "success") toast.success(t("settings.billing.payment.returnedSuccess"));
    else if (outcome === "cancel") toast.info(t("settings.billing.payment.returnedCancel"));
    params.delete("payment");
    const search = params.toString();
    window.history.replaceState(window.history.state, "", `${window.location.pathname}${search ? `?${search}` : ""}`);
    // Handle the redirect once.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  const config = data?.config ?? null;
  const overview = overviewOverride ?? data?.overview ?? null;
  const loading = bundle.loading || !enabled;
  const display = displayOf(config);
  const userRef = useMemo(
    () => ({
      planId: typeof user?.subscriptionPlanID === "number" ? (user.subscriptionPlanID as number) : undefined,
      tier: user?.subscriptionTier ?? "",
    }),
    [user],
  );
  const ctx: BillingContext = { config, plans: data?.plans ?? [], overview, display, loading, user: userRef };
  const topupVisible = paymentEnabled(config);

  const reloadAll = useCallback(() => {
    bundle.reload();
    activity.reload();
    setLogKey((k) => k + 1);
    void reloadUser();
  }, [bundle, activity, reloadUser]);

  return (
    <SettingsPage title={t("settings.nav.subscription")} description={t("settings.billing.description")}>
      {data?.firstError ? (
        <LoadErrorNotice title={t("settings.billing.loadFailed")} error={data.firstError} onRetry={reloadAll} retrying={bundle.loading} />
      ) : null}
      <BillingSummary ctx={ctx} topupVisible={topupVisible} onRedeem={() => setRedeemOpen(true)} onTopup={() => setTopupOpen(true)} onPlans={() => setPlansOpen(true)} />
      <ActivityHeatmap days={activity.data ?? []} loading={activity.loading && !activity.data} />
      <UsageTrend daily={data?.daily ?? []} monthly={data?.monthly ?? []} loading={bundle.loading && !data} display={display} view={view} onView={setView} />
      <UsageLog display={display} refreshKey={logKey} />

      <PlansDialog
        open={plansOpen}
        onOpenChange={setPlansOpen}
        ctx={ctx}
        onPaid={(target) => {
          setPlansOpen(false);
          setPayment(target);
        }}
        onSubscribed={reloadAll}
      />
      <PaymentDialog target={payment} onOpenChange={(open) => !open && setPayment(null)} ctx={ctx} />
      <TopupDialog open={topupOpen} onOpenChange={setTopupOpen} ctx={ctx} />
      <RedeemDialog
        open={redeemOpen}
        onOpenChange={setRedeemOpen}
        onRedeemed={(next) => {
          setOverviewOverride(next);
          void reloadUser();
        }}
      />
    </SettingsPage>
  );
}
