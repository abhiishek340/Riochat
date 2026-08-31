"use client";

import { RefreshCw } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast-store";
import { isAbortError } from "@/lib/api/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { formatDateTime } from "../lib/format";
import { SearchBox } from "../components/toolbar";
import { loadOfficial } from "./api";
import { convertOfficial, searchCatalog, suggest, type Converted, type OfficialCatalog, type OfficialItem } from "./official";

/** Suggests entries of the public OpenRouter catalog for a model and turns the chosen one into prices. */
export function OfficialDialog({ open, modelName, vendor, onOpenChange, onApply }: { open: boolean; modelName: string; vendor: string; onOpenChange: (open: boolean) => void; onApply: (converted: Converted) => void }) {
  const t = useT();
  const { locale } = useI18n();
  const message = useErrorMessage();
  const [catalog, setCatalog] = useState<OfficialCatalog | null>(null);
  const [loading, setLoading] = useState(false);
  const [query, setQuery] = useState("");
  const [picked, setPicked] = useState<OfficialItem | null>(null);
  const [multiplier, setMultiplier] = useState("1");

  const load = async (refresh: boolean, signal?: AbortSignal) => {
    setLoading(true);
    try {
      const data = await loadOfficial(refresh, signal);
      setCatalog(data);
      if (data.stale || data.origin === "builtin") {
        toast.error(t("adminBilling.pricing.official.stale"), { description: data.origin === "builtin" ? t("adminBilling.pricing.official.snapshot", { date: formatDateTime(data.fetchedAt, locale) }) : undefined });
      } else if (refresh) {
        toast.success(t("adminBilling.pricing.official.refreshed", { count: data.items.length }));
      }
    } catch (error) {
      if (!isAbortError(error)) toast.error(t("adminBilling.pricing.official.loadFailed"), { description: message(error) });
    } finally {
      setLoading(false);
    }
  };

  useEffect(() => {
    if (!open) return;
    setQuery("");
    setPicked(null);
    setMultiplier("1");
    const controller = new AbortController();
    void load(false, controller.signal);
    return () => controller.abort();
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const list = useMemo(() => {
    const items = catalog?.items ?? [];
    return query.trim() ? searchCatalog(query, items) : suggest(modelName, vendor, items).map((r) => r.item);
  }, [query, catalog, modelName, vendor]);
  const factor = Number(multiplier);
  const validFactor = Number.isFinite(factor) && factor > 0;
  const conversion = picked ? convertOfficial(picked, validFactor ? factor : 1) : null;

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("adminBilling.pricing.official.title", { name: modelName })}</DialogTitle>
          <DialogDescription>{t("adminBilling.pricing.official.body")}</DialogDescription>
        </DialogHeader>
        <div className="flex flex-wrap items-center gap-2">
          <SearchBox value={query} onChange={setQuery} placeholder={t("adminBilling.pricing.official.search")} className="sm:w-72" />
          <Button variant="outline" size="sm" loading={loading} onClick={() => void load(true)}>
            <RefreshCw />
            {t("adminBilling.pricing.official.refresh")}
          </Button>
          <label className="ml-auto flex items-center gap-2 text-sm">
            {t("adminBilling.pricing.official.multiplier")}
            <Input inputMode="decimal" value={multiplier} onChange={(e) => setMultiplier(e.target.value)} aria-invalid={!validFactor || undefined} className="h-8 w-20" />
          </label>
        </div>
        <div className="max-h-64 overflow-auto rounded-lg border">
          {loading && !catalog ? (
            <div className="space-y-2 p-3">
              <Skeleton className="h-8 w-full" />
              <Skeleton className="h-8 w-full" />
            </div>
          ) : list.length === 0 ? (
            <p className="p-6 text-center text-sm text-muted-foreground">{t("adminBilling.pricing.official.none")}</p>
          ) : (
            <ul>
              {list.map((it) => (
                <li key={it.id} className="border-b last:border-0">
                  <button type="button" onClick={() => setPicked(it)} className={`flex w-full items-center justify-between gap-3 px-3 py-2 text-left text-sm hover:bg-muted/50 ${picked?.id === it.id ? "bg-accent/50" : ""}`}>
                    <span className="min-w-0">
                      <span className="block truncate font-medium">{it.name || it.id}</span>
                      <span className="block truncate font-mono text-xs text-muted-foreground">{it.id}</span>
                    </span>
                  </button>
                </li>
              ))}
            </ul>
          )}
        </div>
        {conversion ? (
          conversion.ok ? (
            <div className="space-y-1 rounded-lg border bg-muted/30 p-3 text-sm">
              <div className="font-medium">{conversion.value.mode === "tiered" ? t("adminBilling.pricing.official.tiered") : t("adminBilling.pricing.official.token")}</div>
              <div className="tabular-nums text-muted-foreground">
                {t("adminBilling.pricing.input")} {conversion.value.input} · {t("adminBilling.pricing.output")} {conversion.value.output} · {t("adminBilling.pricing.cacheRead")} {conversion.value.cacheRead} · {t("adminBilling.pricing.cacheWrite")} {conversion.value.cacheWrite}
              </div>
              {conversion.value.isFree ? <div>{t("adminBilling.pricing.official.free")}</div> : null}
              {conversion.value.ignored.length > 0 ? <div className="text-muted-foreground">{t("adminBilling.pricing.official.ignored", { fields: conversion.value.ignored.join(", ") })}</div> : null}
            </div>
          ) : (
            <p role="alert" className="text-sm font-medium text-destructive">
              {t("adminBilling.pricing.official.notApplicable", { fields: conversion.missing.join(", ") })}
            </p>
          )
        ) : null}
        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button
            disabled={!conversion?.ok || !validFactor}
            onClick={() => {
              if (conversion?.ok) {
                onApply(conversion.value);
                onOpenChange(false);
              }
            }}
          >
            {t("adminBilling.pricing.official.apply")}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
