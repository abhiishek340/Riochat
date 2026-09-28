"use client";

import { Trash2 } from "lucide-react";
import { usePathname, useRouter, useSearchParams } from "next/navigation";
import { useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Field, FieldHint, FieldLabel } from "@/components/ui/field";
import { Input } from "@/components/ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { useCapabilities } from "@/lib/capabilities/provider";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { toast } from "@/components/ui/toast-store";
import { AdminPage } from "../components/page";
import { AdminSection, useIsSuperAdmin } from "../components/section-guard";
import { OptionSelect } from "../components/toolbar";
import { cleanupLogs } from "./api";
import { ModerationTab } from "./moderation-tab";
import { CLEANUP_OF, cleanupBefore, initialTab, positiveInt, visibleTabs, type CleanupType, type LogTab } from "./query";
import { AuditTab, AuthTab, ConversationTab, ErrorsTab, OrdersTab, RedemptionsTab, UsageTab } from "./tabs";

export function AdminLogsPage() {
  return (
    <AdminSection>
      <LogsContent />
    </AdminSection>
  );
}

function LogsContent() {
  const t = useT();
  const router = useRouter();
  const pathname = usePathname();
  const params = useSearchParams();
  const { flags } = useCapabilities();
  const superAdmin = useIsSuperAdmin();
  const tabs = useMemo(() => visibleTabs({ multiUser: flags.multiUser, billingGating: flags.billingGating, contentModeration: flags.contentModeration, superAdmin }), [flags, superAdmin]);
  const requested = params.get("tab");
  const [tab, setTab] = useState<LogTab>(() => initialTab(requested, tabs));
  const [remountKey, setRemountKey] = useState(0);
  const [cleaning, setCleaning] = useState(false);
  const codeId = positiveInt(params.get("code_id"));
  const active: LogTab = tabs.includes(tab) ? tab : "audit";

  const choose = (next: string) => {
    setTab(next as LogTab);
    const search = new URLSearchParams(params.toString());
    search.set("tab", next);
    if (next !== "redemptions") search.delete("code_id");
    router.replace(`${pathname}?${search.toString()}`, { scroll: false });
  };

  const cleanupTypes = tabs.map((x) => CLEANUP_OF[x]).filter((x): x is CleanupType => x !== null);

  return (
    <AdminPage
      title={t("adminLogs.title")}
      description={t("adminLogs.description")}
      actions={
        <Button variant="outline" onClick={() => setCleaning(true)}>
          <Trash2 />
          {t("adminLogs.cleanup.open")}
        </Button>
      }
    >
      <Tabs value={active} onValueChange={choose}>
        <TabsList className="h-auto max-w-full flex-wrap justify-start">
          {tabs.map((id) => (
            <TabsTrigger key={id} value={id}>
              {t(`adminLogs.tabs.${id}`)}
            </TabsTrigger>
          ))}
        </TabsList>
        <TabsContent value={active} className="mt-3">
          {active === "audit" ? <AuditTab remountKey={remountKey} /> : null}
          {active === "usage" ? <UsageTab remountKey={remountKey} /> : null}
          {active === "auth" ? <AuthTab remountKey={remountKey} /> : null}
          {active === "orders" ? <OrdersTab remountKey={remountKey} /> : null}
          {active === "redemptions" ? <RedemptionsTab remountKey={remountKey} codeId={codeId} /> : null}
          {active === "conversation" ? <ConversationTab remountKey={remountKey} /> : null}
          {active === "moderation" ? <ModerationTab remountKey={remountKey} /> : null}
          {active === "errors" ? <ErrorsTab remountKey={remountKey} /> : null}
        </TabsContent>
      </Tabs>
      <CleanupDialog
        open={cleaning}
        types={cleanupTypes}
        onOpenChange={setCleaning}
        onDone={() => setRemountKey((n) => n + 1)}
      />
    </AdminPage>
  );
}

const HIGH_RISK: readonly CleanupType[] = ["usage", "orders"];

function CleanupDialog({ open, types, onOpenChange, onDone }: { open: boolean; types: CleanupType[]; onOpenChange: (open: boolean) => void; onDone: () => void }) {
  const t = useT();
  const message = useErrorMessage();
  const [type, setType] = useState<CleanupType>("audit");
  const [day, setDay] = useState("");
  const [busy, setBusy] = useState(false);
  const chosen: CleanupType = types.includes(type) ? type : (types[0] ?? "audit");
  const before = cleanupBefore(day);
  const today = new Date();
  const max = `${today.getFullYear()}-${String(today.getMonth() + 1).padStart(2, "0")}-${String(today.getDate()).padStart(2, "0")}`;

  const reset = () => {
    setType("audit");
    setDay("");
  };

  const run = async () => {
    if (!before || busy) return;
    setBusy(true);
    try {
      const result = await cleanupLogs({ type: chosen, before });
      toast.success(t("adminLogs.cleanup.done", { count: result.deletedCount }));
      reset();
      onOpenChange(false);
      onDone();
    } catch (error) {
      toast.error(t("adminLogs.cleanup.failed"), { description: message(error) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !busy && onOpenChange(next)}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("adminLogs.cleanup.title")}</DialogTitle>
          <DialogDescription>{t("adminLogs.cleanup.body")}</DialogDescription>
        </DialogHeader>
        <div className="grid gap-4">
          <Field>
            <FieldLabel>{t("adminLogs.cleanup.type")}</FieldLabel>
            <OptionSelect value={chosen} onChange={(v) => setType(v as CleanupType)} options={types.map((x) => ({ value: x, label: t(`adminLogs.cleanup.types.${x}`) }))} className="w-full" />
          </Field>
          <div className="flex items-start gap-2 rounded-lg border bg-muted/40 p-3 text-sm">
            {HIGH_RISK.includes(chosen) ? <Badge tone="danger">{t("adminLogs.cleanup.highRisk")}</Badge> : null}
            <p className="text-muted-foreground">{t(`adminLogs.cleanup.impact.${chosen}`)}</p>
          </div>
          <Field>
            <FieldLabel>{t("adminLogs.cleanup.before")}</FieldLabel>
            <Input type="date" value={day} max={max} onChange={(e) => setDay(e.target.value)} />
            <FieldHint>{day && !before ? t("adminLogs.cleanup.invalidDay") : t("adminLogs.cleanup.boundary")}</FieldHint>
          </Field>
        </div>
        <DialogFooter>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button variant="destructive" loading={busy} disabled={!before} onClick={run}>
            {t("adminLogs.cleanup.confirm")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
