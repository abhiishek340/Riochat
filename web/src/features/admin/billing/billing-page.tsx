"use client";

import { useCallback, useEffect, useState } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { LoadError } from "../components/load-error";
import { useCapabilities } from "@/lib/capabilities/provider";
import { useT } from "@/lib/i18n/provider";
import { AdminPage } from "../components/page";
import { AdminSection } from "../components/section-guard";
import { invalidateReference, loadAllModelPrices, loadAllModels, loadBillingConfig, loadPermissionGroups, loadPlans } from "../lib/reference";
import { useNotify } from "../lib/use-notify";
import { DEFAULT_BILLING_CONFIG, type BillingConfig, type BillingPlan, type ModelPrice, type PermissionGroup, type PlatformModel } from "../lib/wire";
import { CodesSection } from "./codes-section";
import { ConfigSection } from "./config-section";
import { PlansSection } from "./plans-section";
import { PricingSection } from "./pricing-section";
import { McpToolsPricing, NativeToolsSection } from "./tool-pricing";

export function AdminBillingPage() {
  return (
    <AdminSection>
      <BillingContent />
    </AdminSection>
  );
}

function BillingContent() {
  const t = useT();
  const notify = useNotify();
  const { flags } = useCapabilities();
  const [config, setConfig] = useState<BillingConfig>(DEFAULT_BILLING_CONFIG);
  const [plans, setPlans] = useState<BillingPlan[]>([]);
  const [models, setModels] = useState<PlatformModel[]>([]);
  const [prices, setPrices] = useState<ModelPrice[]>([]);
  const [groups, setGroups] = useState<PermissionGroup[]>([]);
  const [ready, setReady] = useState(false);
  const [failed, setFailed] = useState(false);
  const [nonce, setNonce] = useState(0);

  // Reference data loads together and reports a failure once.
  useEffect(() => {
    let live = true;
    setFailed(false);
    Promise.all([loadBillingConfig(), flags.billingGating ? loadPlans() : Promise.resolve([]), loadAllModels(), loadAllModelPrices(), flags.multiUser ? loadPermissionGroups() : Promise.resolve([])]).then(
      ([c, p, m, pr, g]) => {
        if (!live) return;
        setConfig(c);
        setPlans(p);
        setModels(m);
        setPrices(pr);
        setGroups(g);
        setFailed(false);
        setReady(true);
      },
      (error: unknown) => {
        if (!live) return;
        setReady(true);
        setFailed(true);
        notify.error("adminBilling.loadFailed", error);
      },
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [nonce, flags.billingGating, flags.multiUser]);

  const reload = useCallback(() => {
    invalidateReference();
    setNonce((n) => n + 1);
  }, []);

  return (
    <AdminPage title={t("adminBilling.title")} description={t("adminBilling.description")}>
      {!ready ? (
        <Skeleton className="h-64 w-full" />
      ) : failed ? (
        // Defaults would look like a real "self-hosted" configuration, so nothing editable is shown.
        <LoadError message={t("adminBilling.loadFailed")} onRetry={reload} />
      ) : (
        <>
          <ConfigSection config={config} onConfig={setConfig} />
          {flags.billingGating ? (
            <>
              <CodesSection config={config} plans={plans} />
              <PlansSection plans={plans} groups={groups} onChanged={reload} />
            </>
          ) : null}
          <PricingSection config={config} models={models} prices={prices} onPrices={(update) => setPrices(update)} />
          <NativeToolsSection config={config} onConfig={setConfig} />
          <McpToolsPricing />
        </>
      )}
    </AdminPage>
  );
}
