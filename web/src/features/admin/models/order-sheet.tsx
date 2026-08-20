"use client";

import { ArrowDown, ArrowUp, GripVertical } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { LoadError } from "../components/load-error";
import { ModelIcon } from "../components/model-icon";
import { Sheet } from "../components/sheet";
import { loadAllPages } from "../lib/paging";
import { moveItem, sameOrder } from "../lib/reorder";
import { invalidateModels } from "../lib/reference";
import { useNotify } from "../lib/use-notify";
import type { PlatformModel } from "../lib/wire";
import { listModels, saveModelOrder } from "../llm-api";
import { flattenOrder, groupForOrder, type OrderGroup } from "./rules";

/**
 * Orders the models that are available now. Groups and the models inside a
 * group move by drag or by the arrow buttons; saving sends the flattened order.
 */
export function OrderSheet({ open, onOpenChange, onSaved }: { open: boolean; onOpenChange: (open: boolean) => void; onSaved: () => void }) {
  const t = useT();
  const notify = useNotify();
  const message = useErrorMessage();
  const [groups, setGroups] = useState<OrderGroup[] | null>(null);
  const [initial, setInitial] = useState<number[]>([]);
  const [busy, setBusy] = useState(false);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const [announce, setAnnounce] = useState("");
  const [dragGroup, setDragGroup] = useState<number | null>(null);
  const [dragModel, setDragModel] = useState<{ group: number; index: number } | null>(null);

  useEffect(() => {
    if (!open) return;
    let live = true;
    setGroups(null);
    setFailed(false);
    loadAllPages<PlatformModel>((page, size) => listModels({ page, pageSize: size, q: "", status: "", vendor: "", protocol: "", upstream: "", sort: "sortOrder_asc", onlyAvailable: true })).then(
      (list) => {
        if (!live) return;
        const g = groupForOrder(list);
        setGroups(g);
        setInitial(flattenOrder(g));
      },
      (error: unknown) => {
        if (!live) return;
        setGroups([]);
        setFailed(true);
        notify.error("adminModels.toast.loadFailed", error);
      },
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, attempt]);

  const order = useMemo(() => (groups ? flattenOrder(groups) : []), [groups]);
  const dirty = groups !== null && !sameOrder(order, initial);

  const moveGroup = (from: number, to: number) => {
    const name = groups?.[from]?.name;
    setGroups((g) => (g ? moveItem(g, from, to) : g));
    if (name) setAnnounce(t("adminCommon.reorder.moved", { name, position: to + 1, total: groups?.length ?? 0 }));
  };
  const moveModel = (gi: number, from: number, to: number) => {
    const name = groups?.[gi]?.models[from]?.platformModelName;
    setGroups((g) => (g ? g.map((grp, i) => (i === gi ? { ...grp, models: moveItem(grp.models, from, to) } : grp)) : g));
    if (name) setAnnounce(t("adminCommon.reorder.moved", { name, position: to + 1, total: groups?.[gi]?.models.length ?? 0 }));
  };

  const save = async () => {
    if (!dirty) return;
    setBusy(true);
    try {
      await saveModelOrder(order);
      invalidateModels();
      toast.success(t("adminModels.order.saved"));
      onSaved();
      onOpenChange(false);
    } catch (error) {
      toast.error(t("adminModels.order.saveFailed"), { description: message(error) });
    } finally {
      setBusy(false);
    }
  };

  return (
    <Sheet
      open={open}
      onOpenChange={onOpenChange}
      busy={busy}
      size="md"
      title={t("adminModels.order.title")}
      description={t("adminModels.order.description")}
      footer={
        <>
          <Button variant="outline" disabled={busy} onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button loading={busy} disabled={!dirty} onClick={save}>
            {t("common.actions.save")}
          </Button>
        </>
      }
    >
      <span role="status" aria-live="polite" className="sr-only">
        {announce}
      </span>
      {groups === null ? (
        <div className="space-y-2">
          {Array.from({ length: 6 }, (_, i) => (
            <Skeleton key={i} className="h-10 w-full" />
          ))}
        </div>
      ) : failed ? (
        <LoadError message={t("adminModels.toast.loadFailed")} onRetry={() => setAttempt((n) => n + 1)} />
      ) : groups.length === 0 ? (
        <p className="py-8 text-center text-sm text-muted-foreground">{t("adminModels.order.empty")}</p>
      ) : (
        <ol className="space-y-4" aria-label={t("adminModels.order.title")}>
          {groups.map((group, gi) => (
            <li
              key={group.key}
              draggable
              onDragStart={(e) => {
                if (dragModel) return;
                setDragGroup(gi);
                e.dataTransfer.effectAllowed = "move";
              }}
              onDragOver={(e) => dragGroup !== null && e.preventDefault()}
              onDrop={(e) => {
                if (dragGroup === null) return;
                e.preventDefault();
                moveGroup(dragGroup, gi);
                setDragGroup(null);
              }}
              onDragEnd={() => setDragGroup(null)}
              className="rounded-lg border bg-card"
            >
              <div className="flex items-center gap-2 border-b bg-muted/40 px-2 py-1.5">
                <GripVertical aria-hidden className="size-4 cursor-grab text-muted-foreground" />
                <ModelIcon icon={group.icon} name={group.name} size={18} />
                <span className="min-w-0 flex-1 truncate text-sm font-semibold">{group.name}</span>
                <Button variant="ghost" size="icon-xs" disabled={gi === 0} aria-label={`${t("adminCommon.reorder.moveUp")}: ${group.name}`} onClick={() => moveGroup(gi, gi - 1)}>
                  <ArrowUp />
                </Button>
                <Button variant="ghost" size="icon-xs" disabled={gi === groups.length - 1} aria-label={`${t("adminCommon.reorder.moveDown")}: ${group.name}`} onClick={() => moveGroup(gi, gi + 1)}>
                  <ArrowDown />
                </Button>
              </div>
              <ul className="divide-y">
                {group.models.map((m, mi) => (
                  <li
                    key={m.id}
                    draggable
                    onDragStart={(e) => {
                      e.stopPropagation();
                      setDragModel({ group: gi, index: mi });
                      e.dataTransfer.effectAllowed = "move";
                    }}
                    onDragOver={(e) => dragModel && dragModel.group === gi && e.preventDefault()}
                    onDrop={(e) => {
                      if (!dragModel || dragModel.group !== gi) return;
                      e.preventDefault();
                      e.stopPropagation();
                      moveModel(gi, dragModel.index, mi);
                      setDragModel(null);
                    }}
                    onDragEnd={() => setDragModel(null)}
                    className="flex items-center gap-2 px-2 py-1.5"
                  >
                    <GripVertical aria-hidden className="size-4 cursor-grab text-muted-foreground" />
                    <ModelIcon icon={m.icon || m.vendorIcon} name={m.platformModelName} size={18} />
                    <span className="min-w-0 flex-1 truncate text-sm">{m.platformModelName}</span>
                    <Button variant="ghost" size="icon-xs" disabled={mi === 0} aria-label={`${t("adminCommon.reorder.moveUp")}: ${m.platformModelName}`} onClick={() => moveModel(gi, mi, mi - 1)}>
                      <ArrowUp />
                    </Button>
                    <Button variant="ghost" size="icon-xs" disabled={mi === group.models.length - 1} aria-label={`${t("adminCommon.reorder.moveDown")}: ${m.platformModelName}`} onClick={() => moveModel(gi, mi, mi + 1)}>
                      <ArrowDown />
                    </Button>
                  </li>
                ))}
              </ul>
            </li>
          ))}
        </ol>
      )}
    </Sheet>
  );
}
