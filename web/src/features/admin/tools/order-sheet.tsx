"use client";

import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { isAbortError } from "@/lib/api/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { toast } from "@/components/ui/toast-store";
import { SortableList } from "../components/sortable-list";
import { LoadError } from "../components/load-error";
import { Sheet } from "../components/sheet";
import { sameOrder } from "../lib/reorder";
import { listServerTools, saveOrder, type McpServer, type McpTool, type OrderResult } from "./api";
import { sortServers, sortTools, toolLabel } from "./tool-list";

export function OrderSheet({ open, servers, onOpenChange, onSaved }: { open: boolean; servers: McpServer[]; onOpenChange: (open: boolean) => void; onSaved: (results: OrderResult[]) => void }) {
  const t = useT();
  const { locale } = useI18n();
  const message = useErrorMessage();
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [order, setOrder] = useState<McpServer[]>([]);
  const [toolsBy, setToolsBy] = useState<Record<number, McpTool[]>>({});
  const [initial, setInitial] = useState<{ servers: number[]; tools: Record<number, number[]> }>({ servers: [], tools: {} });
  const [current, setCurrent] = useState<number | null>(null);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);

  useEffect(() => {
    if (!open) return;
    const controller = new AbortController();
    const sorted = sortServers(servers);
    setOrder(sorted);
    setCurrent(sorted[0]?.id ?? null);
    setLoading(true);
    setFailed(false);
    Promise.all(sorted.map((s) => listServerTools(s.id, controller.signal)))
      .then((lists) => {
        const by: Record<number, McpTool[]> = {};
        sorted.forEach((s, i) => (by[s.id] = sortTools(lists[i] ?? [], "custom", locale)));
        setToolsBy(by);
        setInitial({ servers: sorted.map((s) => s.id), tools: Object.fromEntries(Object.entries(by).map(([id, list]) => [id, list.map((x) => x.id)])) });
        setLoading(false);
      })
      .catch((error: unknown) => {
        if (isAbortError(error)) return;
        setLoading(false);
        setFailed(true);
        toast.error(t("adminTools.toast.toolsLoadFailed"), { description: message(error) });
      });
    return () => controller.abort();
    // The list is read once per opening.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, attempt]);

  const dirty = useMemo(() => {
    if (!sameOrder(order.map((s) => s.id), initial.servers)) return true;
    return Object.entries(toolsBy).some(([id, list]) => !sameOrder(list.map((x) => x.id), initial.tools[Number(id)] ?? []));
  }, [order, toolsBy, initial]);

  const save = async () => {
    setSaving(true);
    try {
      const results = await saveOrder(order.map((s) => ({ serverID: s.id, toolIDs: (toolsBy[s.id] ?? []).map((x) => x.id) })));
      toast.success(t("adminTools.order.saved"));
      onSaved(results);
      onOpenChange(false);
    } catch (error) {
      toast.error(t("adminTools.toast.orderFailed"), { description: message(error) });
    } finally {
      setSaving(false);
    }
  };

  const active = order.find((s) => s.id === current) ?? null;
  const tools = current !== null ? (toolsBy[current] ?? []) : [];

  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      busy={saving}
      size="lg"
      title={t("adminTools.order.title")}
      description={t("adminTools.order.description")}
      footer={
        <>
          <Button variant="outline" disabled={saving} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={saving} disabled={!dirty || loading || failed} onClick={save}>
            {t("adminTools.order.save")}
          </Button>
        </>
      }
    >
      {loading ? (
        <div className="space-y-2" role="status" aria-label={t("adminTools.order.loading")}>
          {Array.from({ length: 5 }, (_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : failed ? (
        // Without the loaded lists the order would be saved with empty tool lists.
        <LoadError message={t("adminTools.toast.toolsLoadFailed")} onRetry={() => setAttempt((n) => n + 1)} />
      ) : (
        <div className="grid gap-6 md:grid-cols-2">
          <div className="space-y-2">
            <h3 className="text-sm font-semibold">{t("adminTools.order.servers")}</h3>
            <SortableList items={order} itemKey={(s) => s.id} onChange={setOrder} activeKey={current} onSelect={(s) => setCurrent(s.id)} itemLabel={(s) => s.name} render={(s) => <span className="truncate font-medium">{s.name}</span>} />
          </div>
          <div className="space-y-2">
            <h3 className="text-sm font-semibold">{t("adminTools.order.tools", { name: active?.name ?? "" })}</h3>
            {current !== null ? (
              <SortableList
                items={tools}
                itemKey={(x) => x.id}
                onChange={(list) => setToolsBy((by) => ({ ...by, [current]: list }))}
                itemLabel={toolLabel}
                render={(x) => <span className="truncate">{toolLabel(x)}</span>}
              />
            ) : null}
          </div>
        </div>
      )}
    </Sheet>
  );
}
