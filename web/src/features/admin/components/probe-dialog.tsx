"use client";

import { ChevronLeft, ChevronRight, Trash2 } from "lucide-react";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { ConfirmDialog } from "@/components/ui/confirm-dialog";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useT } from "@/lib/i18n/provider";
import { formatLatency } from "../lib/format";
import { useI18n } from "@/lib/i18n/provider";
import { protocolLabel } from "../lib/protocols";
import type { ProbeResult } from "../llm-api";
import { DetailList, DetailSection } from "./detail-list";
import { JsonView } from "./json-view";
import { Mono } from "./page";
import { StatusBadge } from "./status";

/** Shows probe results one at a time. A failed route can be removed from here after confirmation. */
export function ProbeDialog({ results, title, onClose, onDeleteRoute }: { results: ProbeResult[] | null; title?: string; onClose: () => void; onDeleteRoute?: (result: ProbeResult) => Promise<void> }) {
  const t = useT();
  const { locale } = useI18n();
  const [list, setList] = useState<ProbeResult[]>([]);
  const [index, setIndex] = useState(0);
  const [removing, setRemoving] = useState<ProbeResult | null>(null);

  useEffect(() => {
    if (results) {
      setList(results);
      setIndex(0);
    }
  }, [results]);

  const current = list[Math.min(index, Math.max(0, list.length - 1))];
  const open = results !== null && list.length > 0;
  const debug = current?.debug;

  return (
    <>
      <Dialog open={open} onOpenChange={(next) => !next && onClose()}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{title ?? t("adminCommon.probe.title")}</DialogTitle>
            <DialogDescription>{t("adminCommon.probe.count", { index: Math.min(index, list.length - 1) + 1, total: list.length })}</DialogDescription>
          </DialogHeader>
          {current ? (
            <div className="space-y-4">
              <div className="flex items-center justify-between gap-2">
                <StatusBadge value={current.status} prefix="adminCommon.status" />
                <div className="flex items-center gap-1">
                  <Button variant="outline" size="icon-sm" disabled={index <= 0} aria-label={t("adminCommon.common.previous")} onClick={() => setIndex((i) => Math.max(0, i - 1))}>
                    <ChevronLeft />
                  </Button>
                  <Button variant="outline" size="icon-sm" disabled={index >= list.length - 1} aria-label={t("adminCommon.common.next")} onClick={() => setIndex((i) => Math.min(list.length - 1, i + 1))}>
                    <ChevronRight />
                  </Button>
                  {onDeleteRoute && !current.success && current.routeID ? (
                    <Button variant="outline" size="sm" onClick={() => setRemoving(current)}>
                      <Trash2 className="text-destructive" />
                      {t("adminCommon.probe.deleteRoute")}
                    </Button>
                  ) : null}
                </div>
              </div>
              <DetailList
                items={[
                  [t("adminCommon.probe.model"), current.platformModelName],
                  [t("adminCommon.probe.upstream"), current.upstreamName],
                  [t("adminCommon.probe.upstreamModel"), current.upstreamModelName],
                  [t("adminCommon.probe.protocol"), protocolLabel(current.protocol)],
                  [t("adminCommon.probe.endpoint"), current.endpoint ? <Mono key="e" className="break-all">{current.endpoint}</Mono> : ""],
                  [t("adminCommon.probe.latency"), formatLatency(current.latencyMS, locale)],
                  [t("adminCommon.probe.httpStatus"), current.upstreamStatusCode ? String(current.upstreamStatusCode) : ""],
                  current.errorCode || current.errorMessage ? [t("adminCommon.probe.error"), `${current.errorCode ?? ""} ${current.errorMessage ?? ""}`.trim()] : null,
                ]}
              />
              {debug ? (
                <DetailSection title={t("adminCommon.probe.debug")}>
                  <JsonView text={JSON.stringify(debug)} maxHeight="max-h-72" />
                </DetailSection>
              ) : null}
            </div>
          ) : null}
        </DialogContent>
      </Dialog>
      <ConfirmDialog
        open={removing !== null}
        onOpenChange={(next) => !next && setRemoving(null)}
        title={t("adminCommon.probe.deleteTitle")}
        description={t("adminCommon.probe.deleteBody")}
        confirmLabel={t("common.actions.delete")}
        destructive
        onConfirm={async () => {
          if (!removing || !onDeleteRoute) return;
          await onDeleteRoute(removing);
          const next = list.filter((r) => r.routeID !== removing.routeID);
          setList(next);
          setIndex((i) => Math.min(i, Math.max(0, next.length - 1)));
          if (next.length === 0) onClose();
        }}
      />
    </>
  );
}
