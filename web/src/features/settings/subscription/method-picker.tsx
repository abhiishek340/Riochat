"use client";

import { CreditCard, Wallet } from "lucide-react";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { formatPayable, payableFor, type Display } from "./money";
import { sameMethod, type MethodOption, type MethodSelection } from "./payment-logic";

function labelOf(option: MethodOption, t: ReturnType<typeof useT>): string {
  const l = option.label;
  if ("stripe" in l) return t("settings.billing.methods.stripe");
  if ("name" in l) return l.name;
  if ("known" in l) return t(`settings.billing.methods.${l.known}`);
  return t("settings.billing.methods.custom", { type: l.custom });
}

/** Radio-style list of payment methods, each showing what it would charge. */
export function MethodPicker({
  options,
  selection,
  onSelect,
  usd,
  display,
  disabled,
  locale,
}: {
  options: MethodOption[];
  selection: MethodSelection;
  onSelect: (s: MethodSelection) => void;
  /** Amount to pay, in USD. */
  usd: number;
  display: Display;
  disabled?: boolean;
  locale: string;
}) {
  const t = useT();
  return (
    <div role="radiogroup" aria-label={t("settings.billing.payment.method")} className="grid gap-2">
      {options.map((o) => {
        const active = sameMethod(selection, o);
        const Icon = o.provider === "stripe" ? CreditCard : Wallet;
        return (
          <button
            key={`${o.provider}:${o.epayType ?? ""}`}
            type="button"
            role="radio"
            aria-checked={active}
            disabled={disabled}
            onClick={() => onSelect({ provider: o.provider, epayType: o.epayType ?? selection.epayType })}
            className={cn(
              "flex items-center justify-between gap-3 rounded-lg border bg-background px-3.5 py-2.5 text-left text-sm outline-none transition-colors hover:bg-accent/40 focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50",
              active && "border-primary bg-accent/50 shadow-[0_0_0_1px_var(--color-primary)]",
            )}
          >
            <span className="flex items-center gap-2.5 font-medium">
              <Icon className="size-4 text-muted-foreground" />
              {labelOf(o, t)}
            </span>
            <span className="tabular-nums text-muted-foreground">{formatPayable(payableFor(o.provider, usd, display), locale)}</span>
          </button>
        );
      })}
    </div>
  );
}
