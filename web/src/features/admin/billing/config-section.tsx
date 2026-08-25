"use client";

import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { Checkbox } from "../components/checkbox";
import { CopyButton } from "../components/copy-button";
import { Mono, Section, SettingRow } from "../components/page";
import { SettingsGroup } from "../components/settings-group";
import { fieldId, type FieldDef } from "../lib/settings-form";
import { useNotify } from "../lib/use-notify";
import { useSettingsForm } from "../lib/use-settings-form";
import type { BillingConfig, BillingMode } from "../lib/wire";
import { patchConfig } from "./api";
import { DEFAULT_EPAY_TYPES, P, PROVIDERS, parseProviders, stripeWebhookUrl, toggleProvider, validatePayments } from "./payments";

const MODES: BillingMode[] = ["self", "period", "usage"];

const B = "billing";
const PAYMENT_FIELDS: FieldDef[] = [
  { ns: B, key: "payment_providers", type: "string", label: "adminBilling.payments.providers", def: "disabled", blankDefault: true },
  { ns: B, key: "stripe_publishable_key", type: "string", label: "adminBilling.payments.stripePublishable", def: "", visible: (d) => parseProviders(d[P.providers]).includes("stripe") },
  { ns: B, key: "stripe_secret_key", type: "password", label: "adminBilling.payments.stripeSecret", secret: true, visible: (d) => parseProviders(d[P.providers]).includes("stripe") },
  { ns: B, key: "stripe_webhook_secret", type: "password", label: "adminBilling.payments.stripeWebhook", secret: true, visible: (d) => parseProviders(d[P.providers]).includes("stripe") },
  { ns: B, key: "epay_gateway_url", type: "string", label: "adminBilling.payments.epayUrl", def: "", visible: (d) => parseProviders(d[P.providers]).includes("epay") },
  { ns: B, key: "epay_types", type: "json", label: "adminBilling.payments.epayTypes", hint: "adminBilling.payments.epayTypesHint", def: DEFAULT_EPAY_TYPES, blankDefault: true, visible: (d) => parseProviders(d[P.providers]).includes("epay") },
  { ns: B, key: "epay_pid", type: "string", label: "adminBilling.payments.epayPid", def: "", visible: (d) => parseProviders(d[P.providers]).includes("epay") },
  { ns: B, key: "epay_key", type: "password", label: "adminBilling.payments.epayKey", secret: true, visible: (d) => parseProviders(d[P.providers]).includes("epay") },
];
const PROVIDER_FIELD = PAYMENT_FIELDS[0] as FieldDef;
const PAYMENT_ROWS = PAYMENT_FIELDS.slice(1);

export function ConfigSection({ config, onConfig }: { config: BillingConfig; onConfig: (config: BillingConfig) => void }) {
  const t = useT();
  const notify = useNotify();
  const [rate, setRate] = useState("");
  const [prepaid, setPrepaid] = useState("");
  const [saving, setSaving] = useState(false);
  const shownRate = config.usdToCNYRate > 0 ? String(config.usdToCNYRate) : "0";
  const shownPrepaid = config.prepaidAmountUSD > 0 ? String(config.prepaidAmountUSD) : "0";

  useEffect(() => {
    setRate(shownRate);
    setPrepaid(shownPrepaid);
  }, [shownRate, shownPrepaid]);

  const changeMode = async (mode: BillingMode) => {
    if (mode === config.mode) return;
    const before = config;
    onConfig({ ...config, mode });
    try {
      onConfig(await patchConfig({ mode }));
      notify.success("adminBilling.config.modeChanged", { mode: t(`adminBilling.config.modes.${mode}`) });
    } catch (error) {
      onConfig(before);
      notify.error("adminBilling.config.modeFailed", error);
    }
  };

  const changeCurrency = async (displayCurrency: "USD" | "CNY") => {
    if (displayCurrency === config.displayCurrency) return;
    const before = config;
    onConfig({ ...config, displayCurrency });
    try {
      onConfig(await patchConfig({ mode: config.mode, displayCurrency }));
    } catch (error) {
      onConfig(before);
      notify.error("adminBilling.config.currencyFailed", error);
    }
  };

  const dirty = rate.trim() !== shownRate || prepaid.trim() !== shownPrepaid;
  const saveRates = async () => {
    const r = Number(rate.trim());
    if (!Number.isFinite(r) || r <= 0) {
      toast.error(t("adminBilling.config.saveFailed"), { description: t("adminBilling.config.problems.rate") });
      return;
    }
    const p = Number(prepaid.trim());
    if (config.mode !== "self" && (prepaid.trim() === "" || !Number.isFinite(p) || p < 0)) {
      toast.error(t("adminBilling.config.saveFailed"), { description: t("adminBilling.config.problems.prepaid") });
      return;
    }
    setSaving(true);
    try {
      onConfig(await patchConfig({ mode: config.mode, usdToCNYRate: r, ...(config.mode === "self" ? {} : { prepaidAmountUSD: p }) }));
      notify.success("adminBilling.config.saved");
    } catch (error) {
      notify.error("adminBilling.config.saveFailed", error);
    } finally {
      setSaving(false);
    }
  };

  const form = useSettingsForm({ fields: PAYMENT_FIELDS });
  const providers = parseProviders(form.draft[P.providers]);
  const webhook = typeof window === "undefined" ? "" : stripeWebhookUrl(window.location.origin);

  return (
    <>
      <Section title={t("adminBilling.config.title")} description={t("adminBilling.config.description")}>
        <div className="divide-y">
          <SettingRow label={t("adminBilling.config.mode")} hint={t("adminBilling.config.modeHint")}>
            <div role="radiogroup" aria-label={t("adminBilling.config.mode")} className="inline-flex rounded-lg bg-muted p-1">
              {MODES.map((mode) => (
                <button
                  key={mode}
                  type="button"
                  role="radio"
                  aria-checked={config.mode === mode}
                  onClick={() => void changeMode(mode)}
                  className={`rounded-md px-3 py-1 text-sm font-medium transition-colors ${config.mode === mode ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
                >
                  {t(`adminBilling.config.modes.${mode}`)}
                </button>
              ))}
            </div>
          </SettingRow>
          <SettingRow label={t("adminBilling.config.currency")} hint={t("adminBilling.config.currencyHint")}>
            <div role="radiogroup" aria-label={t("adminBilling.config.currency")} className="inline-flex rounded-lg bg-muted p-1">
              {(["USD", "CNY"] as const).map((c) => (
                <button
                  key={c}
                  type="button"
                  role="radio"
                  aria-checked={config.displayCurrency === c}
                  onClick={() => void changeCurrency(c)}
                  className={`rounded-md px-3 py-1 text-sm font-medium transition-colors ${config.displayCurrency === c ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground"}`}
                >
                  {c}
                </button>
              ))}
            </div>
          </SettingRow>
          <SettingRow label={t("adminBilling.config.rate")} hint={t("adminBilling.config.rateHint")} htmlFor="bill-rate">
            <Input id="bill-rate" inputMode="decimal" value={rate} onChange={(e) => setRate(e.target.value)} className="w-32 text-right tabular-nums" />
          </SettingRow>
          {config.mode !== "self" ? (
            <SettingRow label={t("adminBilling.config.prepaid")} hint={t("adminBilling.config.prepaidHint")} htmlFor="bill-prepaid">
              <Input id="bill-prepaid" inputMode="decimal" value={prepaid} onChange={(e) => setPrepaid(e.target.value)} className="w-32 text-right tabular-nums" />
            </SettingRow>
          ) : null}
        </div>
        {dirty ? (
          <div className="mt-4 flex justify-end gap-2 border-t pt-4">
            <Button
              variant="outline"
              onClick={() => {
                setRate(shownRate);
                setPrepaid(shownPrepaid);
              }}
            >
              {t("adminCommon.settings.revert")}
            </Button>
            <Button loading={saving} onClick={saveRates}>
              {t("adminCommon.settings.save")}
            </Button>
          </div>
        ) : null}
      </Section>

      <SettingsGroup
        form={form}
        fields={PAYMENT_ROWS}
        title={t("adminBilling.payments.title")}
        description={t("adminBilling.payments.description")}
        save={{ force: [PROVIDER_FIELD, ...PAYMENT_ROWS], validate: (d, configured) => validatePayments(d, configured) }}
        before={
          <div className="space-y-3 border-b pb-3">
            <div className="text-sm font-medium">{t("adminBilling.payments.providers")}</div>
            <div className="flex flex-wrap gap-6">
              {PROVIDERS.map((p) => (
                <label key={p} className="flex items-center gap-2 text-sm">
                  <Checkbox checked={providers.includes(p)} disabled={form.loading || form.saving} onCheckedChange={(on) => form.set(fieldId(PROVIDER_FIELD), toggleProvider(form.draft[P.providers], p, on))} />
                  {t(`adminBilling.payments.provider.${p}`)}
                </label>
              ))}
            </div>
            {providers.includes("stripe") ? (
              <div className="space-y-1 rounded-lg border bg-muted/30 p-3">
                <div className="text-[13px] font-medium">{t("adminBilling.payments.webhook")}</div>
                <div className="flex items-center gap-1">
                  <Mono className="min-w-0 flex-1 break-all">{webhook}</Mono>
                  <CopyButton text={webhook} />
                </div>
                <p className="text-[13px] text-muted-foreground">{t("adminBilling.payments.webhookHint")}</p>
              </div>
            ) : null}
          </div>
        }
      />
    </>
  );
}

