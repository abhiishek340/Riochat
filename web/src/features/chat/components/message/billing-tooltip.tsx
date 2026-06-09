"use client";

import { useT } from "@/lib/i18n/provider";
import { buildBreakdown, formatMoney, showsFreeNote, tokenLineParts, type BillingLine, type Currency } from "../../lib/billing";
import type { ChatMessage } from "../../types";

const LABEL: Record<BillingLine["kind"], string> = {
  input: "chatMsg.billing.input",
  output: "chatMsg.billing.output",
  cacheRead: "chatMsg.billing.cacheRead",
  cacheWrite: "chatMsg.billing.cacheWrite",
  cacheWrite5m: "chatMsg.billing.cacheWrite5m",
  cacheWrite1h: "chatMsg.billing.cacheWrite1h",
  call: "chatMsg.billing.call",
  duration: "chatMsg.billing.duration",
};

export function BillingDetails({ message, currency, locale }: { message: ChatMessage; currency: Currency; locale: string }) {
  const t = useT();
  const b = buildBreakdown(message.billing, message.usage);
  if (!b) return null;
  const total = formatMoney(b.totalNano, currency);
  return (
    <div className="grid gap-1 text-left text-xs">
      {b.lines.map((line, i) => {
        const label = t(LABEL[line.kind] as "chatMsg.billing.input");
        if (line.kind === "call") return <div key={i}>{label}: {formatMoney(line.rateNano, currency)} × {line.count ?? 1} = {formatMoney(line.amountNano, currency)}</div>;
        if (line.kind === "duration") return <div key={i}>{label}: {line.seconds ?? 0}s × {formatMoney(line.rateNano, currency)}/s = {formatMoney(line.amountNano, currency)}</div>;
        const p = tokenLineParts(line, currency, locale);
        return (
          <div key={i}>
            {label}: {p.tokens} {t("chatMsg.billing.tokens")} × {p.rate} / 1M = {p.amount}
          </div>
        );
      })}
      {b.tier ? <div>{b.tier.upTo === null ? t("chatMsg.billing.tierOpen", { from: new Intl.NumberFormat(locale).format(b.tier.from) }) : t("chatMsg.billing.tierRange", { from: new Intl.NumberFormat(locale).format(b.tier.from), to: new Intl.NumberFormat(locale).format(b.tier.upTo) })}</div> : null}
      {b.items.map((it, i) => (
        <div key={`it-${i}`}>
          {it.name} × {it.count} = {formatMoney(it.amountNano, currency)}
        </div>
      ))}
      {b.rateMultiplier !== 1 ? <div className="text-muted-foreground">{t("chatMsg.billing.multiplier", { multiplier: Number(b.rateMultiplier.toFixed(4)) })}</div> : null}
      {b.schedule ? <div className="text-muted-foreground">{t("chatMsg.billing.schedule", { period: b.schedule.name, percent: b.schedule.percent })}</div> : null}
      {b.fastMode ? <div className="text-muted-foreground">{t("chatMsg.billing.fast")}</div> : null}
      {b.serviceTier ? <div className="text-muted-foreground">{t("chatMsg.billing.tier", { tier: b.serviceTier })}</div> : null}
      {b.cacheTimeout && (message.usage.cacheWrite > 0) ? <div className="text-muted-foreground">{t("chatMsg.billing.cacheNote", { timeout: b.cacheTimeout })}</div> : null}
      <div className="border-t pt-1 font-medium">
        {t("chatMsg.billing.total")}: {showsFreeNote(b) ? t("chatMsg.billing.free") : total}
      </div>
    </div>
  );
}
