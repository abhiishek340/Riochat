"use client";

import { Plus, Sparkles, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldError, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { ModelIcon } from "../components/model-icon";
import { OptionSelect } from "../components/toolbar";
import { useSnapshot } from "../lib/use-snapshot";
import type { ModelPrice } from "../lib/wire";
import { savePrice } from "./api";
import { OfficialDialog } from "./official-dialog";
import type { Converted } from "./official";
import { bodyFromForm, CACHE_BASES, formFromPrice, isVideoModel, PRICING_MODES, type PricingForm, type TierForm } from "./pricing";
import { ScheduleEditor } from "./schedule-editor";
import { validateSchedule } from "./schedule";

export interface PricedRow {
  name: string;
  vendor: string;
  vendorName: string;
  icon: string;
  kindsJSON: string;
}

export function PricingDialog({ row, price, onOpenChange, onSaved }: { row: PricedRow | null; price: ModelPrice | undefined; onOpenChange: (open: boolean) => void; onSaved: (price: ModelPrice) => void }) {
  const t = useT();
  const message = useErrorMessage();
  const shown = useSnapshot(row);
  const [form, setForm] = useState<PricingForm>(formFromPrice(undefined));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [official, setOfficial] = useState(false);

  useEffect(() => {
    if (row) {
      setForm(formFromPrice(price));
      setError(null);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [row]);

  const video = isVideoModel(shown?.kindsJSON);
  const modes = useMemo(() => PRICING_MODES.filter((m) => m !== "duration" || video), [video]);
  const set = <K extends keyof PricingForm>(key: K, value: PricingForm[K]) => setForm((f) => ({ ...f, [key]: value }));
  const setTier = (index: number, patch: Partial<TierForm>) => set("tiers", form.tiers.map((x, i) => (i === index ? { ...x, ...patch } : x)));
  const scheduleIssues = validateSchedule(form.schedule);

  const apply = (c: Converted) => {
    setForm((f) => ({
      ...f,
      isFree: c.isFree,
      mode: c.mode,
      input: String(c.input),
      cacheRead: String(c.cacheRead),
      cacheWrite: String(c.cacheWrite),
      output: String(c.output),
      cacheWriteBasis: c.cacheWriteBasis,
      tiers: c.tiers.length > 0 ? c.tiers : f.tiers,
    }));
  };

  const save = async () => {
    if (!shown || busy) return;
    if (scheduleIssues.length > 0) {
      setError(t("adminBilling.pricing.scheduleBlocked"));
      return;
    }
    setBusy(true);
    setError(null);
    try {
      onSaved(await savePrice(bodyFromForm(shown.name, form)));
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };

  const priceInput = (label: string, key: "input" | "cacheRead" | "cacheWrite" | "output" | "call" | "duration", hint?: string) => (
    <Field>
      <FieldLabel>{label}</FieldLabel>
      <Input inputMode="decimal" value={form[key]} onChange={(e) => set(key, e.target.value)} className="tabular-nums" />
      {hint ? <FieldHint>{hint}</FieldHint> : null}
    </Field>
  );

  return (
    <>
      <Dialog open={row !== null} onOpenChange={(next) => !busy && onOpenChange(next)}>
        <DialogContent className="max-w-3xl">
          <DialogHeader>
            <DialogTitle className="flex items-center gap-2">
              {shown ? <ModelIcon icon={shown.icon} name={shown.name} size={22} /> : null}
              {shown?.name}
            </DialogTitle>
            <DialogDescription>{t("adminBilling.pricing.editBody")}</DialogDescription>
          </DialogHeader>
          <div className="grid gap-5">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <label className="flex items-center gap-3 text-sm font-medium">
                <Switch checked={form.isFree} onCheckedChange={(on) => set("isFree", on)} />
                {t("adminBilling.pricing.free")}
              </label>
              <Button type="button" variant="outline" size="sm" onClick={() => setOfficial(true)}>
                <Sparkles />
                {t("adminBilling.pricing.official.open")}
              </Button>
            </div>
            <Field>
              <FieldLabel>{t("adminBilling.pricing.mode")}</FieldLabel>
              <OptionSelect value={form.mode} onChange={(v) => set("mode", v as PricingForm["mode"])} options={modes.map((m) => ({ value: m, label: t(`adminBilling.pricing.modes.${m}`) }))} className="w-full sm:w-64" />
              <FieldHint>{t(`adminBilling.pricing.modeHints.${form.mode}`)}</FieldHint>
            </Field>

            {form.mode === "token" ? (
              <div className="grid gap-4 sm:grid-cols-2">
                {priceInput(t("adminBilling.pricing.input"), "input", t("adminBilling.pricing.perMillion"))}
                {priceInput(t("adminBilling.pricing.output"), "output", t("adminBilling.pricing.perMillion"))}
                {priceInput(t("adminBilling.pricing.cacheRead"), "cacheRead", t("adminBilling.pricing.perMillion"))}
                {priceInput(t("adminBilling.pricing.cacheWrite"), "cacheWrite", t("adminBilling.pricing.perMillion"))}
                <Field>
                  <FieldLabel>{t("adminBilling.pricing.cacheBasis")}</FieldLabel>
                  <OptionSelect value={form.cacheWriteBasis} onChange={(v) => set("cacheWriteBasis", v)} options={CACHE_BASES.map((b) => ({ value: b, label: t(`adminBilling.pricing.bases.${b}`) }))} className="w-full" />
                </Field>
              </div>
            ) : null}
            {form.mode === "call" ? priceInput(t("adminBilling.pricing.call"), "call", t("adminBilling.pricing.perCall")) : null}
            {form.mode === "duration" ? priceInput(t("adminBilling.pricing.duration"), "duration", t("adminBilling.pricing.perSecond")) : null}
            {form.mode === "tiered" ? (
              <div className="space-y-3">
                <p className="text-[13px] text-muted-foreground">{t("adminBilling.pricing.tiersHint")}</p>
                {form.tiers.map((tier, index) => (
                  <div key={index} className="grid gap-3 rounded-lg border p-3 sm:grid-cols-[8rem_repeat(4,1fr)_auto] sm:items-end">
                    <label className="grid gap-1 text-[13px] font-medium">
                      {t("adminBilling.pricing.tierUpTo")}
                      <Input inputMode="numeric" value={tier.upToTokens} onChange={(e) => setTier(index, { upToTokens: e.target.value })} />
                    </label>
                    {(["input", "cacheRead", "cacheWrite", "output"] as const).map((key) => (
                      <label key={key} className="grid gap-1 text-[13px] font-medium">
                        {t(`adminBilling.pricing.${key}`)}
                        <Input inputMode="decimal" value={tier[key]} onChange={(e) => setTier(index, { [key]: e.target.value })} />
                      </label>
                    ))}
                    <Button variant="ghost" size="icon-sm" disabled={form.tiers.length <= 1} aria-label={t("adminBilling.pricing.removeTier")} onClick={() => set("tiers", form.tiers.filter((_, i) => i !== index))}>
                      <Trash2 className="text-destructive" />
                    </Button>
                  </div>
                ))}
                <Button type="button" variant="outline" size="sm" onClick={() => set("tiers", [...form.tiers, { upToTokens: "0", input: "0", cacheRead: "0", cacheWrite: "0", output: "0" }])}>
                  <Plus />
                  {t("adminBilling.pricing.addTier")}
                </Button>
              </div>
            ) : null}

            <div className="space-y-2">
              <h3 className="text-sm font-semibold">{t("adminBilling.pricing.schedule.title")}</h3>
              <ScheduleEditor periods={form.schedule} onChange={(periods) => set("schedule", periods)} />
            </div>
            <FieldError>{error}</FieldError>
          </div>
          <DialogFooter>
            <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button loading={busy} disabled={scheduleIssues.length > 0} onClick={save}>
              {t("common.actions.save")}
            </Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
      <OfficialDialog open={official} modelName={shown?.name ?? ""} vendor={shown?.vendor ?? ""} onOpenChange={setOfficial} onApply={apply} />
    </>
  );
}
