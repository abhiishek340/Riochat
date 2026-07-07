"use client";

import { useEffect, useMemo, useState } from "react";
import { Bot, Check, ChevronDown, Image as ImageIcon, Search, Sparkles } from "lucide-react";
import { Badge, Popover, PopoverContent, PopoverTrigger, Skeleton } from "@/components/ui";
import { useIsMobile } from "@/hooks/use-media-query";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { formatMoney, type Currency } from "../../lib/billing";
import { modelLabel } from "../../lib/model-caps";
import { currentPeriod, formatContext, groupModels, matchesModel, modalitiesOf } from "../../lib/model-groups";
import { modelsResource } from "../../state/resources";
import type { ModelDTO, PricingDTO } from "../../types";

function ModelIcon({ model, className }: { model: ModelDTO; className?: string }) {
  const src = model.icon || model.vendorIcon;
  if (src && /^(https?:\/\/|\/)/.test(src)) {
    // eslint-disable-next-line @next/next/no-img-element -- admin-provided icon
    return <img src={src} alt="" className={cn("size-4 rounded-sm object-contain", className)} referrerPolicy="no-referrer" />;
  }
  return <Bot className={cn("size-4 text-muted-foreground", className)} />;
}

function usd(v: number, currency: Currency): string {
  return formatMoney(v * 1_000_000_000, currency);
}

function Pricing({ pricing, currency, locale }: { pricing: PricingDTO; currency: Currency; locale: string }) {
  const t = useT();
  if (pricing.isFree) return <p className="text-xs text-muted-foreground">{t("chatModel.freeDescription")}</p>;
  const periods = pricing.schedulePeriods ?? [];
  const now = currentPeriod(periods, pricing.scheduleUTCOffsetMinutes, new Date());
  const line = (label: string, value: number) => (value > 0 ? <div className="flex justify-between gap-3"><span>{label}</span><span className="tabular-nums">{usd(value, currency)}</span></div> : null);
  const days = (w: number[]) => (w.length === 0 || w.length === 7 ? t("chatModel.everyDay") : w.map((d) => t(`chatModel.weekday.d${d}` as "chatModel.weekday.d1")).join(", "));
  return (
    <div className="grid gap-1 text-xs">
      {pricing.mode === "call" ? <div className="flex justify-between"><span>{t("chatModel.perCall")}</span><span className="tabular-nums">{usd(pricing.callUSDPerCall, currency)}</span></div> : null}
      {pricing.mode === "duration" ? <div className="flex justify-between"><span>{t("chatModel.perSecond")}</span><span className="tabular-nums">{usd(pricing.durationUSDPerSecond, currency)}</span></div> : null}
      {pricing.mode !== "call" && pricing.mode !== "duration" ? (
        <>
          <p className="text-muted-foreground">{t("chatModel.perMillion")}</p>
          {line(t("chatModel.price.input"), pricing.inputUSDPerMTokens)}
          {line(t("chatModel.price.output"), pricing.outputUSDPerMTokens)}
          {line(t("chatModel.price.cacheRead"), pricing.cacheReadUSDPerMTokens)}
          {line(t("chatModel.price.cacheWrite"), pricing.cacheWriteUSDPerMTokens)}
          {pricing.cacheWrite5mMultiplier || pricing.cacheWrite1hMultiplier ? <p className="text-muted-foreground">{t("chatModel.cacheWriteNote", { m5: pricing.cacheWrite5mMultiplier ?? 0, m1: pricing.cacheWrite1hMultiplier ?? 0 })}</p> : null}
        </>
      ) : null}
      {pricing.tiers && pricing.tiers.length > 0 ? (
        <table className="mt-1 w-full text-left">
          <thead>
            <tr className="text-muted-foreground">
              <th className="font-normal">{t("chatModel.tier.range")}</th>
              <th className="text-right font-normal">{t("chatModel.price.input")}</th>
              <th className="text-right font-normal">{t("chatModel.price.output")}</th>
            </tr>
          </thead>
          <tbody>
            {pricing.tiers.map((tier, i) => (
              <tr key={i}>
                <td>{new Intl.NumberFormat(locale).format(tier.fromTokens)}{tier.upToTokens === null ? "+" : `–${new Intl.NumberFormat(locale).format(tier.upToTokens)}`}</td>
                <td className="text-right tabular-nums">{usd(tier.inputUSDPerMTokens, currency)}</td>
                <td className="text-right tabular-nums">{usd(tier.outputUSDPerMTokens, currency)}</td>
              </tr>
            ))}
          </tbody>
        </table>
      ) : null}
      {periods.length > 0 ? (
        <div className="mt-1 grid gap-0.5 border-t pt-1.5">
          <p className="text-muted-foreground">{t("chatModel.schedule")}</p>
          {periods.map((p, i) => (
            <div key={i} className={cn("flex justify-between gap-2 rounded px-1", i === now && "bg-accent text-accent-foreground")}>
              <span>{days(p.weekdays)} {p.start}–{p.end}{i === now ? ` · ${t("chatModel.now")}` : ""}</span>
              <span className="tabular-nums">{p.ratePercent}%</span>
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
}

function ModelDetail({ model, currency, locale }: { model: ModelDTO; currency: Currency; locale: string }) {
  const t = useT();
  const mods = modalitiesOf(model);
  const context = formatContext(model.contextWindow);
  return (
    <div className="grid content-start gap-2.5 p-3 text-sm">
      <div className="flex items-center gap-2">
        <ModelIcon model={model} className="size-5" />
        <span className="font-medium">{modelLabel(model.platformModelName)}</span>
        {model.pricing?.isFree ? <Badge tone="success">{t("chatModel.free")}</Badge> : null}
        {model.source === "personal" ? <Badge tone="outline">{t("chatModel.byok")}</Badge> : null}
      </div>
      {model.description ? <p className="text-xs text-muted-foreground">{model.description}</p> : null}
      <div className="grid gap-1 text-xs">
        <div className="flex justify-between"><span className="text-muted-foreground">{t("chatModel.modalities")}</span><span>{mods.map((x) => t(`chatModel.modality.${x}` as "chatModel.modality.text")).join(" · ")}</span></div>
        {context ? <div className="flex justify-between"><span className="text-muted-foreground">{t("chatModel.context")}</span><span>{t("chatModel.contextTokens", { size: context })}</span></div> : null}
      </div>
      {model.pricing ? <Pricing pricing={model.pricing} currency={currency} locale={locale} /> : <p className="text-xs text-muted-foreground">{model.source === "personal" ? t("chatModel.chargedByProvider") : t("chatModel.priceUnknown")}</p>}
    </div>
  );
}

export function ModelPicker({
  models,
  selected,
  loading,
  currency,
  locale,
  onSelect,
  placement = "top",
}: {
  models: ModelDTO[];
  selected: ModelDTO | null;
  loading: boolean;
  currency: Currency;
  locale: string;
  onSelect: (name: string) => void;
  placement?: "top" | "bottom";
}) {
  const t = useT();
  const mobile = useIsMobile();
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const [groupId, setGroupId] = useState<string>("");
  const [hovered, setHovered] = useState<ModelDTO | null>(null);

  const groups = useMemo(() => groupModels(models, t("chatModel.otherGroup")), [models, t]);
  const filtered = useMemo(() => (query.trim() ? groups.map((g) => ({ ...g, models: g.models.filter((m) => matchesModel(m, query)) })).filter((g) => g.models.length > 0) : groups), [groups, query]);

  // The catalogue refreshes every time the picker opens.
  useEffect(() => {
    if (open) {
      void modelsResource.refresh();
      setQuery("");
      setHovered(null);
      const g = groups.find((x) => x.models.some((m) => m.platformModelName === selected?.platformModelName));
      setGroupId(g?.id ?? groups[0]?.id ?? "");
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  const activeGroup = filtered.find((g) => g.id === groupId) ?? filtered[0];
  const detail = hovered ?? (activeGroup?.models.find((m) => m.platformModelName === selected?.platformModelName) ?? activeGroup?.models[0] ?? null);

  if (loading && models.length === 0) return <Skeleton className="h-8 w-32 rounded-lg" />;

  const row = (m: ModelDTO) => (
    <button
      key={m.platformModelName}
      type="button"
      role="option"
      aria-selected={m.platformModelName === selected?.platformModelName}
      onMouseEnter={() => setHovered(m)}
      onFocus={() => setHovered(m)}
      onClick={() => {
        onSelect(m.platformModelName);
        setOpen(false);
      }}
      className={cn("flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm outline-none hover:bg-accent focus-visible:bg-accent", m.platformModelName === selected?.platformModelName && "bg-accent/60")}
    >
      <ModelIcon model={m} />
      <span className="min-w-0 flex-1 truncate">{modelLabel(m.platformModelName)}</span>
      {m.pricing?.isFree ? <Badge tone="success">{t("chatModel.free")}</Badge> : null}
      {m.source === "personal" ? <Badge tone="outline">{t("chatModel.byok")}</Badge> : null}
      {(m.inputModalities ?? []).includes("image") ? <ImageIcon className="size-3.5 text-muted-foreground" aria-label={t("chatModel.modality.image")} /> : null}
      {m.platformModelName === selected?.platformModelName ? <Check className="size-4 text-primary" /> : null}
    </button>
  );

  return (
    <Popover open={open} onOpenChange={setOpen}>
      <PopoverTrigger asChild>
        <button
          type="button"
          aria-label={t("chatModel.select")}
          className="inline-flex h-8 max-w-52 items-center gap-1.5 rounded-lg px-2 text-sm font-medium text-muted-foreground outline-none transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:bg-accent"
        >
          {selected ? <ModelIcon model={selected} /> : <Sparkles className="size-4" />}
          <span className="truncate">{selected ? modelLabel(selected.platformModelName) : t("chatModel.select")}</span>
          <ChevronDown className="size-3.5 shrink-0 opacity-60" />
        </button>
      </PopoverTrigger>
      <PopoverContent align="start" side={placement} className={cn("p-0", mobile ? "w-[min(22rem,calc(100vw-1.5rem))]" : "w-[44rem] max-w-[calc(100vw-2rem)]")}>
        <div className="flex items-center gap-2 border-b px-3 py-2">
          <Search className="size-4 text-muted-foreground" />
          <input
            autoFocus
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={t("chatModel.search")}
            aria-label={t("chatModel.search")}
            className="h-6 flex-1 bg-transparent text-sm outline-none placeholder:text-muted-foreground"
          />
        </div>
        {models.length === 0 || filtered.length === 0 ? (
          <p role="status" className="px-4 py-8 text-center text-sm text-muted-foreground">{t("chatModel.empty")}</p>
        ) : mobile ? (
          <div role="listbox" aria-label={t("chatModel.select")} className="max-h-[60dvh] overflow-auto p-1.5">
            {filtered.map((g) => (
              <div key={g.id}>
                <div className="px-2 pb-1 pt-2 text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{g.name}</div>
                {g.models.map(row)}
              </div>
            ))}
          </div>
        ) : (
          <div className="grid h-[22rem] grid-cols-[10rem_1fr_15rem]">
            <div className="overflow-auto border-r p-1.5" role="tablist" aria-orientation="vertical" aria-label={t("chatModel.group")}>
              {filtered.map((g) => (
                <button
                  key={g.id}
                  type="button"
                  role="tab"
                  aria-selected={g.id === activeGroup?.id}
                  onClick={() => setGroupId(g.id)}
                  className={cn("flex w-full items-center gap-2 rounded-lg px-2 py-1.5 text-left text-sm hover:bg-accent", g.id === activeGroup?.id && "bg-accent")}
                >
                  <span className="min-w-0 flex-1 truncate">{g.name}</span>
                  {g.byok ? <Badge tone="outline">{t("chatModel.byok")}</Badge> : null}
                </button>
              ))}
            </div>
            <div role="listbox" aria-label={t("chatModel.select")} className="overflow-auto p-1.5">
              {activeGroup?.models.map(row)}
            </div>
            <div className="overflow-auto border-l bg-muted/30">{detail ? <ModelDetail model={detail} currency={currency} locale={locale} /> : null}</div>
          </div>
        )}
      </PopoverContent>
    </Popover>
  );
}
