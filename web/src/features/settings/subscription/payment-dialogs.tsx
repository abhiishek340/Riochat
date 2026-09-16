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
