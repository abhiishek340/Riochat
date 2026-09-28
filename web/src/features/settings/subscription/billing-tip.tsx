"use client";

import type { ReactNode } from "react";
import { useI18n, useT } from "@/lib/i18n/provider";
import { formatInt } from "../lib/format";
import { NANO, type Breakdown, type LineKind } from "./billing-breakdown";
import { formatMoney, type Display } from "./money";

/** Nano-USD as a money string (more digits for tiny amounts). */
function money(nano: number, display: Display, locale: string): string {
  return formatMoney(nano / NANO, display, locale);
}

const KIND_KEY: Record<LineKind, "input" | "output" | "cacheRead" | "cacheWrite" | "call" | "duration"> = {
  input: "input",
  output: "output",
  cacheRead: "cacheRead",
  cacheWrite: "cacheWrite",
  call: "call",
  duration: "duration",
};

/**
 * Formula view of a usage entry's cost: one `quantity * unit price = amount`
 * line per component, then service items and the total.
 */
export function BillingTip({ breakdown, display, scope }: { breakdown: Breakdown; display: Display; scope: "all" | "services" }) {
  const t = useT();
  const { locale } = useI18n();
  const b = breakdown;
  const showModel = scope === "all";
  const notes: ReactNode[] = [];
  if (showModel && b.multiplier !== null) notes.push(t("settings.billing.log.tip.multiplier", { value: b.multiplier }));
  if (showModel && b.schedule) notes.push(t("settings.billing.log.tip.schedule", { name: b.schedule }));
  if (showModel && b.cacheWriteVariant) notes.push(t(`settings.billing.log.tip.cacheWriteVariants.${b.cacheWriteVariant}`));
  if (showModel && b.tier) {
    notes.push(
      b.tier.upTo === null
        ? t("settings.billing.log.tip.tierFrom", { from: formatInt(b.tier.from, locale) })
        : t("settings.billing.log.tip.tierRange", { from: formatInt(b.tier.from, locale), to: formatInt(b.tier.upTo, locale) }),
    );
  }
  const total = scope === "all" ? b.totalNano : b.serviceNano;
  const freeNote = b.free && b.serviceNano === 0 && scope === "all";

  return (
    <div className="grid min-w-56 max-w-sm gap-1.5 text-left text-xs">
      {notes.map((n, i) => (
        <div key={i} className="opacity-80">
          {n}
        </div>
      ))}
      {showModel
        ? b.lines.map((line) => {
            const per = line.kind === "call" ? t("settings.billing.log.tip.perCall") : line.kind === "duration" ? t("settings.billing.log.tip.perSecond") : t("settings.billing.log.tip.perMillion");
            const unit = line.kind === "call" ? t("settings.billing.log.tip.calls") : line.kind === "duration" ? t("settings.billing.log.tip.seconds") : t("settings.billing.log.tip.tokens");
            return (
              <div key={line.kind} className="grid gap-0.5">
                <span className="opacity-80">{t(`settings.billing.log.tip.kinds.${KIND_KEY[line.kind]}`)}</span>
                <span className="font-mono tabular-nums">
                  {formatInt(line.quantity, locale)} {unit} × {money(line.rateNano, display, locale)} / {per} = {money(freeNote ? 0 : line.billedNano, display, locale)}
                </span>
              </div>
            );
          })
        : null}
      {b.services.map((s, i) => (
        <div key={i} className="grid gap-0.5">
          <span className="opacity-80">{s.name || t("settings.billing.log.tip.baseService")}</span>
          <span className="font-mono tabular-nums">
            {formatInt(s.calls, locale)} {t("settings.billing.log.tip.calls")} × {money(s.unitNano, display, locale)} / {t("settings.billing.log.tip.perCall")} = {money(s.billedNano, display, locale)}
          </span>
        </div>
      ))}
      <div className="mt-0.5 flex items-baseline justify-between gap-4 border-t border-current/20 pt-1.5 font-medium">
        <span>{t("settings.billing.log.tip.total")}</span>
        <span className="font-mono tabular-nums">{money(freeNote ? 0 : total, display, locale)}</span>
      </div>
      {freeNote ? <div className="opacity-80">{t("settings.billing.log.tip.freeModel")}</div> : null}
    </div>
  );
}
