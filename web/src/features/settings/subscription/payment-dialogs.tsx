"use client";

import { Check } from "lucide-react";
import { useEffect, useMemo, useRef, useState, type FormEvent } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { toast } from "@/components/ui/toast-store";
import { resolveApiBase } from "@/lib/api/base-url";
import { publicWebOrigin } from "@/lib/api/platform";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useI18n, useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { createCheckout, redeemCode, subscribeFree } from "./api";
import { MethodPicker } from "./method-picker";
import {
  currencySymbol,
  formatMoney,
  fromDisplay,
  isValidTopup,
  minorUnits,
  normalizeInterval,
  parseAmount,
  priceAmountText,
  type Display,
} from "./money";
import { checkoutUrls, DEFAULT_SELECTION, methodOptions, paymentEnabled, reconcileSelection, type MethodSelection } from "./payment-logic";
import {
  actionDisabled,
  actionKind,
  actionVariant,
  defaultPrice,
  isCurrentPlan,
  planFeatures,
  planRank,
  protectedRank,
  resolveCurrentPlan,
  type ActionKind,
  type UserPlanRef,
} from "./plans-logic";
import type { BillingConfig, Overview, Plan, PlanPrice } from "./types";

export interface BillingContext {
  config: BillingConfig | null;
  plans: Plan[];
  overview: Overview | null;
  display: Display;
  loading: boolean;
  user: UserPlanRef;
}

/** Opens the hosted checkout page in a new tab without a referrer or opener. */
function openCheckout(url: string): void {
  window.open(url, "_blank", "noopener,noreferrer");
}

function useMethods(config: BillingConfig | null) {
  const options = useMemo(() => methodOptions(config), [config]);
  const [selection, setSelection] = useState<MethodSelection>(DEFAULT_SELECTION);
  const effective = useMemo(() => reconcileSelection(selection, options), [selection, options]);
  return { options, selection: effective, setSelection };
}

function checkoutOrigin(): string {
  return publicWebOrigin(resolveApiBase()) || (typeof window !== "undefined" ? window.location.origin : "");
}

// ---- plans -------------------------------------------------------------------

export interface PaymentTarget {
  plan: Plan;
  price: PlanPrice;
  kind: ActionKind;
  protectedHigher: boolean;
}

export function PlansDialog({
  open,
  onOpenChange,
  ctx,
  onPaid,
  onSubscribed,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  ctx: BillingContext;
  /** A paid plan was chosen: the parent opens the payment dialog. */
  onPaid: (target: PaymentTarget) => void;
  /** A free plan was switched to. */
  onSubscribed: () => void;
}) {
  const t = useT();
  const { locale } = useI18n();
  const errorMessage = useErrorMessage();
  const [busyPrice, setBusyPrice] = useState<number | null>(null);

  const current = resolveCurrentPlan(ctx.plans, ctx.overview, ctx.user);
  const rank = protectedRank(current, ctx.overview?.entitlements ?? []);
  const payOn = paymentEnabled(ctx.config);

  const choose = async (plan: Plan, kind: ActionKind, isCurrent: boolean) => {
    const price = defaultPrice(plan);
    if (isCurrent && kind === "current") return;
    if (!price) {
      toast.error(t("settings.billing.plans.unavailableToast"));
      return;
    }
    if (kind === "freeBlocked") {
      toast.error(t("settings.billing.plans.freeBlocked"), { description: t("settings.billing.plans.freeBlockedHint") });
      return;
    }
    if (price.amountCents > 0) {
      if (!payOn) {
        toast.error(t("settings.billing.payment.disabled"));
        return;
      }
      onPaid({ plan, price, kind, protectedHigher: kind === "renew" && rank > planRank(plan) });
      return;
    }
    setBusyPrice(price.id);
    try {
      await subscribeFree(price.id);
      toast.success(t("settings.billing.plans.updated"));
      onOpenChange(false);
      onSubscribed();
    } catch (e) {
      toast.error(t("settings.billing.plans.subscribeFailed"), { description: errorMessage(e) });
    } finally {
      setBusyPrice(null);
    }
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-4xl">
        <DialogHeader>
          <DialogTitle>{t("settings.billing.plans.title")}</DialogTitle>
          <DialogDescription>{t("settings.billing.plans.description")}</DialogDescription>
        </DialogHeader>
        <div className="grid max-h-[70vh] gap-3 overflow-y-auto pr-1 sm:grid-cols-2 lg:grid-cols-3">
          {ctx.plans.map((plan) => {
            const price = defaultPrice(plan);
            const isCurrent = isCurrentPlan(plan, ctx.overview, ctx.user);
            const kind = actionKind({ plan, isCurrent, current, protectedRank: rank });
            const credit = formatMoney(plan.periodCreditUSD, ctx.display, locale);
            const features = planFeatures(plan, [t("settings.billing.plans.creditLine", { credit }), t("settings.billing.plans.freeModelsExcluded")]).slice(0, 6);
            const processing = price !== undefined && busyPrice === price.id;
            return (
              <article key={plan.id} className={cn("grid content-start gap-3 rounded-xl border bg-background p-4", isCurrent && "border-primary shadow-[0_0_0_1px_var(--color-primary)]")}>
                <header className="flex items-start justify-between gap-2">
                  <h3 className="text-base font-semibold">{plan.name || plan.code}</h3>
                  {isCurrent ? <Badge tone="primary">{t("settings.billing.plans.currentBadge")}</Badge> : null}
                </header>
                <p className="text-xl font-semibold tabular-nums">{priceLabel(price, ctx.display, locale, t)}</p>
                <ul className="grid gap-1.5 text-[13px] text-muted-foreground">
                  {features.map((line, i) => (
                    <li key={i} className="flex gap-2">
                      <Check className="mt-0.5 size-3.5 shrink-0 text-success" />
                      <span>{line}</span>
                    </li>
                  ))}
                </ul>
                <Button
                  variant={actionVariant(kind)}
                  disabled={ctx.loading || actionDisabled(kind) || busyPrice !== null}
                  loading={processing}
                  onClick={() => void choose(plan, kind, isCurrent)}
                >
                  {processing ? t("settings.billing.plans.processing") : t(`settings.billing.plans.actions.${kind}`)}
                </Button>
              </article>
            );
          })}
        </div>
      </DialogContent>
    </Dialog>
  );
}

export function priceLabel(price: PlanPrice | undefined, display: Display, locale: string, t: ReturnType<typeof useT>): string {
  if (!price) return "-";
  const interval = normalizeInterval(price.billingInterval);
  return t("settings.billing.plans.priceLine", { amount: priceAmountText(price, display, locale), interval: t(`settings.billing.plans.intervals.${interval}`) });
}

// ---- payment -------------------------------------------------------------------

export function PaymentDialog({
  target,
  onOpenChange,
  ctx,
}: {
  target: PaymentTarget | null;
  onOpenChange: (open: boolean) => void;
  ctx: BillingContext;
}) {
  const t = useT();
  const { locale } = useI18n();
  const errorMessage = useErrorMessage();
  const { options, selection, setSelection } = useMethods(ctx.config);
  const [busy, setBusy] = useState(false);
  const last = useRef<PaymentTarget | null>(null);
  if (target) last.current = target;
  const shown = target ?? last.current;

  const go = async () => {
    if (!target) {
      toast.error(t("settings.billing.payment.noPlan"));
      return;
    }
    setBusy(true);
    try {
      const url = await createCheckout({
        orderType: "subscription",
        priceID: target.price.id,
        cycles: 1,
        paymentProvider: selection.provider,
        epayType: selection.provider === "epay" ? selection.epayType : undefined,
        ...checkoutUrls(checkoutOrigin()),
      });
      if (!url) {
        toast.error(t("settings.billing.payment.checkoutFailed"), { description: t("settings.billing.payment.urlMissing") });
        return;
      }
      openCheckout(url);
      onOpenChange(false);
    } catch (e) {
      toast.error(t("settings.billing.payment.checkoutFailed"), { description: errorMessage(e) });
    } finally {
      setBusy(false);
    }
  };

  const kindKey = shown?.kind === "renew" ? "renew" : shown?.kind === "upgrade" ? "upgrade" : "default";
  const impact = shown?.kind === "renew" ? (shown.protectedHigher ? "renewAfterHigher" : "renew") : shown?.kind === "upgrade" ? "upgrade" : "";
  const usd = shown ? shown.price.amountCents / 100 : 0;

  return (
    <Dialog open={target !== null} onOpenChange={(next) => (busy ? undefined : onOpenChange(next))}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t(`settings.billing.payment.titles.${kindKey}`)}</DialogTitle>
          <DialogDescription>
            {impact ? t(`settings.billing.payment.impact.${impact}`) : t("settings.billing.payment.description")}
          </DialogDescription>
        </DialogHeader>
        {shown ? (
          <p className="rounded-lg bg-muted px-3 py-2 text-sm font-medium">
            {shown.plan.name || shown.plan.code} / {priceLabel(shown.price, ctx.display, locale, t)}
          </p>
        ) : null}
        <MethodPicker options={options} selection={selection} onSelect={setSelection} usd={usd} display={ctx.display} disabled={busy} locale={locale} />
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={busy} disabled={options.length === 0} onClick={() => void go()}>
            {t("common.actions.continue")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

// ---- top-up --------------------------------------------------------------------

export function TopupDialog({ open, onOpenChange, ctx }: { open: boolean; onOpenChange: (open: boolean) => void; ctx: BillingContext }) {
  const t = useT();
  const { locale } = useI18n();
  const errorMessage = useErrorMessage();
  const { options, selection, setSelection } = useMethods(ctx.config);
  const [text, setText] = useState("20");
  const [busy, setBusy] = useState(false);
  const disabled = ctx.loading || busy || !paymentEnabled(ctx.config);

  useEffect(() => {
    if (open) setText("20");
  }, [open]);

  const amount = parseAmount(text);
  const usd = amount !== null && amount > 0 ? fromDisplay(amount, ctx.display) : 0;
  const symbol = currencySymbol(ctx.display.currency);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    if (!isValidTopup(amount)) {
      toast.error(t("settings.billing.topup.invalid"));
      return;
    }
    setBusy(true);
    try {
      const url = await createCheckout({
        orderType: "topup",
        amountMinorUnits: minorUnits(amount),
        cycles: 1,
        paymentProvider: selection.provider,
        epayType: selection.provider === "epay" ? selection.epayType : undefined,
        ...checkoutUrls(checkoutOrigin()),
      });
      if (!url) {
        toast.error(t("settings.billing.payment.checkoutFailed"), { description: t("settings.billing.payment.urlMissing") });
        return;
      }
      openCheckout(url);
      onOpenChange(false);
    } catch (err) {
      toast.error(t("settings.billing.payment.checkoutFailed"), { description: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => (busy ? undefined : onOpenChange(next))}>
      <DialogContent>
        <form noValidate onSubmit={(e) => void submit(e)} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{t("settings.billing.topup.title")}</DialogTitle>
            <DialogDescription>{t("settings.billing.topup.description")}</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel>{t("settings.billing.topup.amount")}</FieldLabel>
            <div className="flex items-center gap-2">
              <span className="text-sm text-muted-foreground">{symbol.trim()}</span>
              <Input type="number" inputMode="decimal" min={0} step={0.01} value={text} disabled={disabled} onChange={(e) => setText(e.target.value)} />
            </div>
            <p className="text-[13px] text-muted-foreground">
              {t("settings.billing.topup.balance", { balance: formatMoney(ctx.overview?.balanceUSD ?? 0, ctx.display, locale) })}
            </p>
          </Field>
          <MethodPicker options={options} selection={selection} onSelect={setSelection} usd={usd} display={ctx.display} disabled={disabled} locale={locale} />
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={busy} disabled={disabled || options.length === 0}>
              {t("common.actions.continue")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}

// ---- redemption ----------------------------------------------------------------

export function RedeemDialog({
  open,
  onOpenChange,
  onRedeemed,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  onRedeemed: (overview: Overview) => void;
}) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    if (!open) setCode("");
  }, [open]);

  const submit = async (e: FormEvent) => {
    e.preventDefault();
    const value = code.trim();
    if (!value) {
      toast.error(t("settings.billing.redeem.invalid"));
      return;
    }
    setBusy(true);
    try {
      onRedeemed(await redeemCode(value));
      setCode("");
      onOpenChange(false);
      toast.success(t("settings.billing.redeem.succeeded"));
    } catch (err) {
      toast.error(t("settings.billing.redeem.failed"), { description: errorMessage(err) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => (busy ? undefined : onOpenChange(next))}>
      <DialogContent>
        <form onSubmit={(e) => void submit(e)} className="grid gap-4">
          <DialogHeader>
            <DialogTitle>{t("settings.billing.redeem.title")}</DialogTitle>
            <DialogDescription>{t("settings.billing.redeem.description")}</DialogDescription>
          </DialogHeader>
          <Field>
            <FieldLabel>{t("settings.billing.redeem.code")}</FieldLabel>
            <Input value={code} className="font-mono" autoComplete="off" spellCheck={false} disabled={busy} onChange={(e) => setCode(e.target.value)} />
          </Field>
          <DialogFooter>
            <Button type="button" variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
              {t("common.actions.cancel")}
            </Button>
            <Button type="submit" loading={busy}>
              {t("settings.billing.redeem.submit")}
            </Button>
          </DialogFooter>
        </form>
      </DialogContent>
    </Dialog>
  );
}
