"use client";

import { Plus, Trash2 } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { canonicalizeTime, emptyPeriod, MAX_PERIODS, validateSchedule, type SchedulePeriod, type ScheduleIssueKind } from "./schedule";

const WEEKDAYS = [1, 2, 3, 4, 5, 6, 0] as const;
const DAY_KEYS = ["sun", "mon", "tue", "wed", "thu", "fri", "sat"] as const;

/** Rows of time-of-day multipliers. Times are tidied when a field loses focus. */
export function ScheduleEditor({ periods, onChange }: { periods: SchedulePeriod[]; onChange: (periods: SchedulePeriod[]) => void }) {
  const t = useT();
  const issues = validateSchedule(periods);
  const kindsOf = (index: number): ScheduleIssueKind[] => issues.filter((i) => i.index === index).map((i) => i.kind);
  const countIssue = issues.some((i) => i.kind === "count");
  const update = (index: number, patch: Partial<SchedulePeriod>) => onChange(periods.map((p, i) => (i === index ? { ...p, ...patch } : p)));

  return (
    <div className="space-y-3">
      {periods.length === 0 ? <p className="text-[13px] text-muted-foreground">{t("adminBilling.pricing.schedule.none")}</p> : null}
      {periods.map((p, index) => {
        const bad = kindsOf(index);
        return (
          <div key={index} className={cn("grid gap-3 rounded-lg border p-3", bad.length > 0 && "border-destructive/60")}>
            <div className="grid gap-3 sm:grid-cols-[1fr_6rem_6rem_7rem_auto] sm:items-end">
              <label className="grid gap-1 text-[13px] font-medium">
                {t("adminBilling.pricing.schedule.name")}
                <Input value={p.name} maxLength={64} onChange={(e) => update(index, { name: e.target.value })} aria-invalid={bad.includes("name") || undefined} />
              </label>
              <label className="grid gap-1 text-[13px] font-medium">
                {t("adminBilling.pricing.schedule.start")}
                <Input value={p.start} onChange={(e) => update(index, { start: e.target.value })} onBlur={() => update(index, { start: canonicalizeTime(p.start) ?? p.start })} aria-invalid={bad.includes("time") || undefined} />
              </label>
              <label className="grid gap-1 text-[13px] font-medium">
                {t("adminBilling.pricing.schedule.end")}
                <Input value={p.end} onChange={(e) => update(index, { end: e.target.value })} onBlur={() => update(index, { end: canonicalizeTime(p.end) ?? p.end })} aria-invalid={bad.includes("time") || undefined} />
              </label>
              <label className="grid gap-1 text-[13px] font-medium">
                {t("adminBilling.pricing.schedule.rate")}
                <Input
                  inputMode="numeric"
                  value={String(p.ratePercent)}
                  onChange={(e) => update(index, { ratePercent: /^\d+$/.test(e.target.value.trim()) ? Number(e.target.value.trim()) : Number.NaN })}
                  aria-invalid={bad.includes("rate") || undefined}
                />
              </label>
              <Button variant="ghost" size="icon-sm" aria-label={t("adminBilling.pricing.schedule.remove")} onClick={() => onChange(periods.filter((_, i) => i !== index))}>
                <Trash2 className="text-destructive" />
              </Button>
            </div>
            <div role="group" aria-label={t("adminBilling.pricing.schedule.days")} className="flex flex-wrap gap-1.5">
              {WEEKDAYS.map((d) => {
                const on = p.weekdays.includes(d);
                return (
                  <button
                    key={d}
                    type="button"
                    aria-pressed={on}
                    onClick={() => update(index, { weekdays: on ? p.weekdays.filter((x) => x !== d) : [...p.weekdays, d] })}
                    className={cn("rounded-md border px-2.5 py-1 text-xs font-medium transition-colors", on ? "border-primary bg-primary/10 text-primary" : "text-muted-foreground hover:bg-muted")}
                  >
                    {t(`adminBilling.pricing.schedule.weekdays.${DAY_KEYS[d]}`)}
                  </button>
                );
              })}
            </div>
            {bad.length > 0 ? (
              <ul className="text-[13px] font-medium text-destructive" role="alert">
                {[...new Set(bad)].map((k) => (
                  <li key={k}>{t(`adminBilling.pricing.schedule.issues.${k}`)}</li>
                ))}
              </ul>
            ) : null}
          </div>
        );
      })}
      {countIssue ? (
        <p role="alert" className="text-[13px] font-medium text-destructive">
          {t("adminBilling.pricing.schedule.issues.count", { max: MAX_PERIODS })}
        </p>
      ) : null}
      <Button type="button" variant="outline" size="sm" onClick={() => onChange([...periods, emptyPeriod()])}>
        <Plus />
        {t("adminBilling.pricing.schedule.add")}
      </Button>
      <p className="text-[13px] text-muted-foreground">{t("adminBilling.pricing.schedule.hint")}</p>
    </div>
  );
}
