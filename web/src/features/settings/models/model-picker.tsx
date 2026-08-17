"use client";

import { Image as ImageIcon, MessageSquare, RefreshCw, Video } from "lucide-react";
import { useEffect, useMemo, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Input, NativeSelect } from "@/components/ui/input";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { Checkbox } from "../parts";
import type { AvailableModel } from "./api";
import { buildProtocolChoices, protocolKind, splitProtocols, withCurrentChoice, type ProtocolKind } from "./protocols";
import { buildRows, filterRows, toggleVisible, visibleCheckState, type PickerRow, type SelectedModel } from "./selection";

const KIND_ICON: Record<ProtocolKind, typeof MessageSquare> = { chat: MessageSquare, image: ImageIcon, video: Video };

/**
 * Staged model chooser: ticks and protocol changes stay local until Confirm;
 * Cancel or Escape throws them away.
 */
export function ModelPicker({
  open,
  onOpenChange,
  available,
  loading,
  error,
  selected,
  offeredProtocols,
  fallbackProtocol,
  onRefresh,
  onConfirm,
}: {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  available: AvailableModel[];
  loading: boolean;
  error: string | null;
  /** The selection when the picker opens (also the saved protocols). */
  selected: SelectedModel[];
  /** Protocols the server lets a model use; empty hides the protocol selects. */
  offeredProtocols: string[];
  fallbackProtocol: string;
  onRefresh?: () => void;
  onConfirm: (selection: SelectedModel[]) => void;
}) {
  const t = useT();
  const [rows, setRows] = useState<PickerRow[]>([]);
  const [ticked, setTicked] = useState<Set<string>>(new Set());
  const [protocols, setProtocols] = useState<Record<string, string>>({});
  const [query, setQuery] = useState("");
  const [onlySelected, setOnlySelected] = useState(false);
  const seeded = useRef(false);

  useEffect(() => {
    if (open) {
      seeded.current = false;
      setTicked(new Set(selected.map((m) => m.name)));
      setProtocols({});
      setRows([]);
      setQuery("");
      setOnlySelected(false);
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  // Order and staged state are fixed at open; a refresh while open keeps the
  // order of rows that were already there.
  useEffect(() => {
    if (!open) return;
    const built = buildRows(available, selected, fallbackProtocol);
    if (!seeded.current && built.length > 0) {
      // The list can arrive after the dialog opened: take the selection then.
      seeded.current = true;
      setTicked(new Set(selected.map((m) => m.name)));
    }
    setRows((previous) => {
      if (previous.length === 0) return built;
      const known = new Map(built.map((r) => [r.name, r]));
      const kept = previous.filter((r) => known.has(r.name)).map((r) => known.get(r.name) as PickerRow);
      const keptNames = new Set(kept.map((r) => r.name));
      return [...kept, ...built.filter((r) => !keptNames.has(r.name))];
    });
    setProtocols((current) => {
      const next = { ...current };
      for (const r of built) if (next[r.name] === undefined) next[r.name] = r.protocol;
      return next;
    });
    // `selected` is read once per open; later changes belong to the dialog.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open, available]);

  const choices = useMemo(() => buildProtocolChoices(offeredProtocols), [offeredProtocols]);
  const perModel = offeredProtocols.length > 0;
  const visible = useMemo(() => filterRows(rows, query, onlySelected, ticked), [rows, query, onlySelected, ticked]);
  const state = visibleCheckState(visible, ticked);
  const selectedCount = rows.filter((r) => ticked.has(r.name)).length;

  const toggleRow = (name: string) =>
    setTicked((current) => {
      const next = new Set(current);
      if (next.has(name)) next.delete(name);
      else next.add(name);
      return next;
    });

  const confirm = () => {
    const out: SelectedModel[] = [];
    for (const r of rows) {
      if (!ticked.has(r.name)) continue;
      const value = protocols[r.name] ?? r.protocol;
      out.push({ name: r.name, protocols: value ? splitProtocols(value) : fallbackProtocol ? [fallbackProtocol] : [] });
    }
    onConfirm(out);
    onOpenChange(false);
  };

  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-2xl">
        <DialogHeader>
          <DialogTitle>{t("settings.models.picker.title")}</DialogTitle>
          <DialogDescription>{t("settings.models.picker.description")}</DialogDescription>
        </DialogHeader>

        <div className="flex flex-wrap items-center gap-2">
          <div className="inline-flex rounded-lg bg-muted p-1 text-sm" role="group" aria-label={t("settings.models.picker.filter")}>
            <button
              type="button"
              aria-pressed={!onlySelected}
              onClick={() => setOnlySelected(false)}
              className={cn("rounded-md px-2.5 py-1 font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring", !onlySelected && "bg-background shadow-sm")}
            >
              {t("settings.models.picker.all", { count: rows.length })}
            </button>
            <button
              type="button"
              aria-pressed={onlySelected}
              onClick={() => setOnlySelected(true)}
              className={cn("rounded-md px-2.5 py-1 font-medium outline-none focus-visible:ring-2 focus-visible:ring-ring", onlySelected && "bg-background shadow-sm")}
            >
              {t("settings.models.picker.selected", { count: selectedCount })}
            </button>
          </div>
          <Input
            className="min-w-40 flex-1"
            value={query}
            placeholder={t("settings.models.picker.search")}
            aria-label={t("settings.models.picker.search")}
            autoComplete="off"
            onChange={(e) => setQuery(e.target.value)}
          />
          {onRefresh ? (
            <Button variant="outline" size="icon" aria-label={t("common.actions.refresh")} disabled={loading} onClick={onRefresh}>
              <RefreshCw className={cn(loading && "animate-spin")} />
            </Button>
          ) : null}
        </div>

        <div className="overflow-hidden rounded-lg border">
          <label className="flex items-center gap-3 border-b bg-muted/50 px-3 py-2 text-sm font-medium">
            <Checkbox
              checked={state === "all"}
              indeterminate={state === "some"}
              disabled={visible.length === 0}
              onCheckedChange={() => setTicked(toggleVisible(visible, ticked))}
              aria-label={t("settings.models.picker.toggleAll")}
            />
            {t("settings.models.picker.toggleAll")}
          </label>
          <div className="max-h-[22rem] min-h-40 overflow-y-auto">
            {loading && rows.length === 0 ? (
              <div role="status" className="flex h-40 items-center justify-center gap-2 text-sm text-muted-foreground">
                <Spinner /> {t("settings.models.picker.loading")}
              </div>
            ) : error && rows.length === 0 ? (
              <div className="grid h-40 place-content-center justify-items-center gap-2 px-4 text-center text-sm">
                <p role="alert" className="text-destructive">
                  {error}
                </p>
                {onRefresh ? (
                  <Button variant="outline" size="sm" onClick={onRefresh}>
                    {t("common.actions.retry")}
                  </Button>
                ) : null}
              </div>
            ) : rows.length === 0 ? (
              <p className="flex h-40 items-center justify-center text-sm text-muted-foreground">{t("settings.models.picker.empty")}</p>
            ) : visible.length === 0 ? (
              <p className="flex h-40 items-center justify-center text-sm text-muted-foreground">
                {onlySelected && !query.trim() ? t("settings.models.picker.noneSelected") : t("settings.models.picker.noMatches")}
              </p>
            ) : (
              <ul>
                {visible.map((row) => {
                  const value = protocols[row.name] ?? row.protocol;
                  const options = withCurrentChoice(choices, value);
                  const Kind = KIND_ICON[protocolKind(splitProtocols(value)[0] ?? "")];
                  return (
                    <li key={row.name} className="flex items-center gap-3 border-b px-3 py-1.5 last:border-0 hover:bg-muted/40">
                      <label className="flex min-w-0 flex-1 cursor-pointer items-center gap-3 py-1">
                        <Checkbox checked={ticked.has(row.name)} onCheckedChange={() => toggleRow(row.name)} />
                        <span className="truncate font-mono text-[13px]">{row.name}</span>
                      </label>
                      {perModel ? (
                        <div className="flex shrink-0 items-center gap-1.5">
                          <Kind className="size-3.5 text-muted-foreground" aria-hidden />
                          <NativeSelect
                            className="h-8 w-52 text-[13px]"
                            aria-label={t("settings.models.picker.protocolFor", { name: row.name })}
                            value={value}
                            onChange={(e) => {
                              setProtocols((current) => ({ ...current, [row.name]: e.target.value }));
                              setTicked((current) => new Set(current).add(row.name));
                            }}
                          >
                            {options.map((c) => (
                              <option key={c.value} value={c.value}>
                                {c.label}
                              </option>
                            ))}
                          </NativeSelect>
                        </div>
                      ) : null}
                    </li>
                  );
                })}
              </ul>
            )}
          </div>
        </div>

        <DialogFooter>
          <Button variant="outline" onClick={() => onOpenChange(false)}>
            {t("common.actions.cancel")}
          </Button>
          <Button disabled={loading || ticked.size === 0} onClick={confirm}>
            {t("settings.models.picker.confirm", { count: ticked.size })}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
