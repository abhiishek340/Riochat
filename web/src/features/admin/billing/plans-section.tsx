"use client";

import { Pencil } from "lucide-react";
import { useEffect, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldError, FieldLabel } from "@/components/ui/field";
import { Input, Textarea } from "@/components/ui/input";
import { Tooltip } from "@/components/ui/tooltip";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { CellActions, DataTable, type Column } from "../components/data-table";
import { Section } from "../components/page";
import { OptionSelect } from "../components/toolbar";
import { formatCents, formatCredit } from "../lib/format";
import { useSnapshot } from "../lib/use-snapshot";
import type { BillingPlan, PermissionGroup } from "../lib/wire";
import { updatePlan } from "./api";

const INTERVALS = ["month", "year", "lifetime"] as const;

export function defaultPrice(plan: BillingPlan) {
  return plan.prices.find((p) => p.isDefault) ?? plan.prices[0];
}

/** Non-numeric or negative input counts as zero. */
export function nonNegative(text: string): number {
  const n = Number(text.trim());
  return Number.isFinite(n) && n > 0 ? n : 0;
}

/** The group a plan starts the form with: its own, else the default group, else the first. */
export function initialGroupId(plan: BillingPlan, groups: readonly PermissionGroup[]): string {
  if (plan.permissionGroupID && groups.some((g) => g.id === plan.permissionGroupID)) return String(plan.permissionGroupID);
  const fallback = groups.find((g) => g.isDefault) ?? groups[0];
  return fallback ? String(fallback.id) : "";
}

export function PlansSection({ plans, groups, onChanged }: { plans: BillingPlan[]; groups: PermissionGroup[]; onChanged: () => void }) {
  const t = useT();
  const { locale } = useI18n();
  const [editing, setEditing] = useState<BillingPlan | null>(null);
  const groupName = (id: number | null) => groups.find((g) => g.id === id)?.name ?? "-";
  const columns: Column<BillingPlan>[] = [
    {
      id: "plan",
      header: t("adminBilling.plans.columns.plan"),
      cell: (p) => (
        <div className="min-w-0">
          <div className="flex items-center gap-2">
            <span className="font-medium">{p.name}</span>
            <Badge tone="outline">{p.code}</Badge>
            {!p.isActive ? <Badge tone="neutral">{t("adminCommon.common.inactive")}</Badge> : null}
          </div>
        </div>
      ),
    },
    { id: "description", header: t("adminBilling.plans.columns.description"), className: "max-w-xs", cell: (p) => <span className="line-clamp-2 text-[13px] text-muted-foreground">{p.description || "-"}</span> },
    {
      id: "price",
      header: t("adminBilling.plans.columns.price"),
      className: "whitespace-nowrap tabular-nums",
      cell: (p) => {
        const price = defaultPrice(p);
        return price ? `${formatCents(price.amountCents, price.currency, locale)} / ${t(`adminBilling.plans.intervals.${price.billingInterval === "year" ? "year" : price.billingInterval === "lifetime" ? "lifetime" : "month"}`)}` : "-";
      },
    },
    { id: "credit", header: t("adminBilling.plans.columns.credit"), className: "tabular-nums", cell: (p) => formatCredit(p.periodCreditUSD, locale) },
    { id: "group", header: t("adminBilling.plans.columns.group"), cell: (p) => groupName(p.permissionGroupID) },
    {
      id: "actions",
      header: <span className="sr-only">{t("adminCommon.table.actions")}</span>,
      cell: (p) => (
        <CellActions>
          <Tooltip label={t("common.actions.edit")}>
            <Button variant="ghost" size="icon-sm" aria-label={`${t("common.actions.edit")}: ${p.name}`} onClick={() => setEditing(p)}>
              <Pencil />
            </Button>
          </Tooltip>
        </CellActions>
      ),
    },
  ];
  return (
    <Section title={t("adminBilling.plans.title")} description={t("adminBilling.plans.description")} flush>
      <div className="p-4">
        <DataTable columns={columns} rows={plans} rowKey={(p) => p.id} empty={t("adminBilling.plans.empty")} label={t("adminBilling.plans.title")} />
      </div>
      <PlanDialog plan={editing} groups={groups} onOpenChange={(open) => !open && setEditing(null)} onSaved={onChanged} />
    </Section>
  );
}

function PlanDialog({ plan, groups, onOpenChange, onSaved }: { plan: BillingPlan | null; groups: PermissionGroup[]; onOpenChange: (open: boolean) => void; onSaved: () => void }) {
  const t = useT();
  const message = useErrorMessage();
  const shown = useSnapshot(plan);
  const [name, setName] = useState("");
  const [description, setDescription] = useState("");
  const [price, setPrice] = useState("0");
  const [interval, setInterval] = useState("month");
  const [credit, setCredit] = useState("0");
  const [group, setGroup] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!plan) return;
    const p = defaultPrice(plan);
    setName(plan.name);
    setDescription(plan.description);
    setPrice(p ? String(p.amountCents / 100) : "0");
    setInterval(p?.billingInterval === "year" || p?.billingInterval === "lifetime" ? p.billingInterval : "month");
    setCredit(String(plan.periodCreditUSD));
    setGroup(initialGroupId(plan, groups));
    setError(null);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [plan]);

  const save = async () => {
    if (!shown) return;
    setBusy(true);
    setError(null);
    try {
      const body: Record<string, unknown> = {
        name: name.trim(),
        description: description.trim(),
        amountUSD: nonNegative(price),
        currency: "USD",
        billingInterval: interval,
        periodCreditUSD: nonNegative(credit),
      };
      const groupId = Number(group);
      if (Number.isInteger(groupId) && groupId > 0) body.permissionGroupID = groupId;
      await updatePlan(shown.id, body);
      onSaved();
      onOpenChange(false);
    } catch (e) {
      setError(message(e));
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={plan !== null} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("adminBilling.plans.editTitle", { name: shown?.name ?? "" })}</DialogTitle>
          <DialogDescription>{t("adminBilling.plans.editBody")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <Field required>
            <FieldLabel>{t("adminBilling.plans.form.name")}</FieldLabel>
            <Input value={name} maxLength={64} onChange={(e) => setName(e.target.value)} />
          </Field>
          <Field>
            <FieldLabel>{t("adminBilling.plans.form.description")}</FieldLabel>
            <Textarea rows={2} value={description} maxLength={255} onChange={(e) => setDescription(e.target.value)} />
          </Field>
          <div className="grid gap-4 sm:grid-cols-2">
            <Field>
              <FieldLabel>{t("adminBilling.plans.form.price")}</FieldLabel>
              <Input inputMode="decimal" value={price} onChange={(e) => setPrice(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>{t("adminBilling.plans.form.interval")}</FieldLabel>
              <OptionSelect value={interval} onChange={setInterval} options={INTERVALS.map((i) => ({ value: i, label: t(`adminBilling.plans.intervals.${i}`) }))} className="w-full" />
            </Field>
            <Field>
              <FieldLabel>{t("adminBilling.plans.form.credit")}</FieldLabel>
              <Input inputMode="decimal" value={credit} onChange={(e) => setCredit(e.target.value)} />
            </Field>
            <Field>
              <FieldLabel>{t("adminBilling.plans.form.group")}</FieldLabel>
              <OptionSelect value={group} onChange={setGroup} options={groups.map((g) => ({ value: String(g.id), label: g.name }))} className="w-full" />
            </Field>
          </div>
          <FieldError>{error}</FieldError>
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={busy} onClick={save}>
            {t("common.actions.save")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
