"use client";

import { useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Skeleton } from "@/components/ui/skeleton";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { SearchBox } from "../components/toolbar";
import { loadAllModels } from "../lib/reference";
import { useNotify } from "../lib/use-notify";
import type { PlatformModel } from "../lib/wire";
import { prettyCaps } from "./caps-doc";
import { parseCaps } from "./capabilities";
import { copySources, presetMatches, PRESETS } from "./presets";

/** Replaces the capabilities with a built-in preset or the capabilities of another model. */
export function PresetsDialog({ open, onOpenChange, selfId, routeProtocols, onApply }: { open: boolean; onOpenChange: (open: boolean) => void; selfId: number | null; routeProtocols: string[]; onApply: (text: string) => void }) {
  const t = useT();
  const notify = useNotify();
  const [models, setModels] = useState<PlatformModel[] | null>(null);
  const [q, setQ] = useState("");

  useEffect(() => {
    if (!open) return;
    let live = true;
    loadAllModels().then(
      (list) => live && setModels(list),
      (error: unknown) => {
        if (live) {
          setModels([]);
          notify.error("adminModels.toast.loadFailed", error);
        }
      },
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const sources = useMemo(() => copySources(models ?? [], selfId, q), [models, selfId, q]);

  const apply = (text: string) => {
    onApply(text);
    toast.success(t("adminModels.presets.applied"));
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-h-[85vh] max-w-xl overflow-y-auto">
        <DialogHeader>
          <DialogTitle>{t("adminModels.presets.title")}</DialogTitle>
          <DialogDescription>{t("adminModels.presets.description")}</DialogDescription>
        </DialogHeader>
        <Tabs defaultValue="presets">
          <TabsList>
            <TabsTrigger value="presets">{t("adminModels.presets.builtIn")}</TabsTrigger>
            <TabsTrigger value="models">{t("adminModels.presets.fromModel")}</TabsTrigger>
          </TabsList>
          <TabsContent value="presets">
            <ul className="space-y-2">
              {PRESETS.map((p) => {
                const fits = presetMatches(p, routeProtocols);
                return (
                  <li key={p.id} className="flex items-start gap-3 rounded-lg border p-3">
                    <div className="min-w-0 flex-1">
                      <div className="flex flex-wrap items-center gap-2">
                        <span className="text-sm font-medium">{t(`adminModels.presets.items.${p.id}.name`)}</span>
                        {!fits ? <Badge tone="warning">{t("adminModels.presets.notMatched")}</Badge> : null}
                      </div>
                      <p className="mt-0.5 text-[13px] text-muted-foreground">{t(`adminModels.presets.items.${p.id}.body`)}</p>
                    </div>
                    <Button size="sm" variant="outline" onClick={() => apply(prettyCaps(p.caps))}>
                      {t("adminModels.presets.use")}
                    </Button>
                  </li>
                );
              })}
            </ul>
          </TabsContent>
          <TabsContent value="models" className="space-y-3">
            <SearchBox value={q} onChange={setQ} placeholder={t("adminModels.presets.search")} className="sm:w-full" />
            {models === null ? (
              <Skeleton className="h-24 w-full" />
            ) : sources.length === 0 ? (
              <p className="py-6 text-center text-sm text-muted-foreground">{t("adminModels.presets.noModels")}</p>
            ) : (
              <ul className="max-h-80 space-y-1.5 overflow-y-auto">
                {sources.map((m) => {
                  const parsed = parseCaps(m.capabilitiesJSON);
                  return (
                    <li key={m.id} className="flex items-center gap-3 rounded-lg border px-3 py-2">
                      <span className="min-w-0 flex-1 truncate text-sm">{m.platformModelName}</span>
                      <Button size="sm" variant="outline" disabled={!parsed.ok} onClick={() => parsed.ok && apply(prettyCaps(parsed.value))}>
                        {t("adminModels.presets.copy")}
                      </Button>
                    </li>
                  );
                })}
              </ul>
            )}
          </TabsContent>
        </Tabs>
      </DialogContent>
    </Dialog>
  );
}
