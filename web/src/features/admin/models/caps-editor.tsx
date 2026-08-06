"use client";

import { Plus, Sparkles, Trash2 } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { Input, NativeSelect, Textarea } from "@/components/ui/input";
import { Switch } from "@/components/ui/switch";
import { useT } from "@/lib/i18n/provider";
import { Checkbox } from "../components/checkbox";
import { Collapsible } from "../components/collapsible";
import { MultiSelectMenu } from "../components/menu-select";
import { protocolLabel, PROTOCOLS } from "../lib/protocols";
import { useNotify } from "../lib/use-notify";
import { loadNativeToolCatalog } from "./api";
import { useCapsDoc, type CapsDoc } from "./caps-doc";
import {
  addCatalogTool,
  allIssues,
  cacheEditable,
  catalogForProtocols,
  CONTROL_TYPES,
  controlRows,
  imageToggleShown,
  nativeRows,
  parseCaps,
  readImageStream,
  readModalities,
  readPromptCache,
  validateControls,
  validateNative,
  writeControls,
  writeImageStream,
  writeModalities,
  writeNative,
  writePromptCache,
  type CacheAvailability,
  type CatalogTool,
  type ControlRow,
  type NativeToolRow,
} from "./capabilities";
import { MODALITIES } from "./reasoning";
import { Labeled } from "./ui";

export function ModalitiesField({ doc, detected, disabled }: { doc: CapsDoc; detected: string[]; disabled?: boolean }) {
  const t = useT();
  const { mode, chosen } = readModalities(doc.caps);
  const off = disabled || !doc.valid;
  return (
    <Labeled group label={t("adminModels.modalities.label")} hint={mode === "auto" ? t("adminModels.modalities.autoHint", { list: detected.length > 0 ? detected.join(", ") : t("adminModels.modalities.unknown") }) : undefined}>
      <div className="flex flex-wrap items-center gap-2">
        <Button type="button" size="sm" variant={mode === "auto" ? "default" : "outline"} disabled={off} onClick={() => doc.update((c) => writeModalities(c, "auto", []))}>
          {t("adminModels.modalities.auto")}
        </Button>
        <Button type="button" size="sm" variant={mode === "custom" ? "default" : "outline"} disabled={off} onClick={() => doc.update((c) => writeModalities(c, "custom", chosen.length > 0 ? chosen : detected))}>
          {t("adminModels.modalities.custom")}
        </Button>
        {mode === "custom" ? (
          <div className="flex flex-wrap gap-x-4 gap-y-1" role="group" aria-label={t("adminModels.modalities.label")}>
            {MODALITIES.map((m) => (
              <label key={m} className="flex items-center gap-1.5 text-sm">
                <Checkbox checked={m === "text" || chosen.includes(m)} disabled={off || m === "text"} onCheckedChange={(on) => doc.update((c) => writeModalities(c, "custom", on ? [...chosen, m] : chosen.filter((x) => x !== m)))} />
                {t(`adminModels.modalities.kinds.${m}`)}
              </label>
            ))}
          </div>
        ) : null}
      </div>
    </Labeled>
  );
}

function ControlsEditor({ rows, onRows, disabled }: { rows: ControlRow[]; onRows: (rows: ControlRow[]) => void; disabled: boolean }) {
  const t = useT();
  const issues = validateControls(rows);
  const issueOf = (index: number) => issues.find((i) => i.index === index)?.issue;
  const set = (index: number, patch: Partial<ControlRow>) => onRows(rows.map((r, i) => (i === index ? { ...r, ...patch } : r)));
  return (
    <div className="space-y-3">
      <p className="text-[13px] text-muted-foreground">{t("adminModels.caps.controlsHint")}</p>
      {rows.map((row, index) => {
        const issue = issueOf(index);
        return (
          <div key={index} className="space-y-2 rounded-lg border p-3">
            <div className="grid gap-2 sm:grid-cols-6">
              <Labeled label={t("adminModels.caps.path")} className="sm:col-span-3">
                <Input value={row.path} disabled={disabled} onChange={(e) => set(index, { path: e.target.value })} placeholder="reasoning.effort" spellCheck={false} className="font-mono text-[12.5px]" />
              </Labeled>
              <Labeled label={t("adminModels.caps.type")} className="sm:col-span-2">
                <NativeSelect value={row.type} disabled={disabled} aria-label={t("adminModels.caps.type")} onChange={(e) => set(index, { type: e.target.value as ControlRow["type"] })}>
                  <option value="">-</option>
                  {CONTROL_TYPES.map((type) => (
                    <option key={type} value={type}>
                      {t(`adminModels.caps.types.${type}`)}
                    </option>
                  ))}
                </NativeSelect>
              </Labeled>
              <div className="flex items-end justify-end">
                <Button type="button" variant="ghost" size="icon-sm" disabled={disabled} aria-label={t("adminCommon.common.remove")} onClick={() => onRows(rows.filter((_, i) => i !== index))}>
                  <Trash2 className="text-destructive" />
                </Button>
              </div>
            </div>
            <div className="grid gap-2 sm:grid-cols-2">
              <Labeled label={t("adminModels.caps.label")}>
                <Input value={row.label} disabled={disabled} onChange={(e) => set(index, { label: e.target.value })} />
              </Labeled>
              <Labeled label={t("adminModels.caps.defaultValue")}>
                <Input value={row.defaultValue} disabled={disabled} onChange={(e) => set(index, { defaultValue: e.target.value })} spellCheck={false} className="font-mono text-[12.5px]" />
              </Labeled>
              {row.type === "select" ? (
                <Labeled label={t("adminModels.caps.options")} hint={t("adminModels.caps.optionsHint")} className="sm:col-span-2">
                  <Input value={row.options} disabled={disabled} onChange={(e) => set(index, { options: e.target.value })} spellCheck={false} />
                </Labeled>
              ) : null}
              <Labeled label={t("adminModels.caps.description")} className="sm:col-span-2">
                <Input value={row.description} disabled={disabled} onChange={(e) => set(index, { description: e.target.value })} />
              </Labeled>
            </div>
            <label className="flex items-center gap-2 text-sm">
              <Switch checked={row.locked} disabled={disabled} onCheckedChange={(on) => set(index, { locked: on })} />
              {t("adminModels.caps.locked")}
              <span className="text-xs text-muted-foreground">{t("adminModels.caps.lockedHint")}</span>
            </label>
            {issue ? <p className="text-[13px] text-destructive">{t(`adminModels.caps.issues.${issue}`, { row: index + 1 })}</p> : null}
          </div>
        );
      })}
      <Button type="button" size="sm" variant="outline" disabled={disabled} onClick={() => onRows([...rows, { path: "", label: "", description: "", type: "text", options: "", defaultValue: "", locked: false }])}>
        <Plus />
        {t("adminModels.caps.addControl")}
      </Button>
    </div>
  );
}

function NativeEditor({ rows, onRows, routeProtocols, disabled }: { rows: NativeToolRow[]; onRows: (rows: NativeToolRow[]) => void; routeProtocols: string[]; disabled: boolean }) {
  const t = useT();
  const notify = useNotify();
  const [catalog, setCatalog] = useState<CatalogTool[] | null>(null);
  useEffect(() => {
    let live = true;
    loadNativeToolCatalog().then(
      (list) => live && setCatalog(list),
      (error: unknown) => {
        if (live) {
          setCatalog([]);
          notify.error("adminModels.caps.catalogFailed", error);
        }
      },
    );
    return () => {
      live = false;
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);
  const offered = useMemo(() => catalogForProtocols(catalog ?? [], routeProtocols), [catalog, routeProtocols]);
  const issues = validateNative(rows);
  const set = (index: number, patch: Partial<NativeToolRow>) => onRows(rows.map((r, i) => (i === index ? { ...r, ...patch } : r)));
  const protocolOptions = PROTOCOLS.map((p) => ({ value: p, label: protocolLabel(p), text: protocolLabel(p) }));
  return (
    <div className="space-y-3">
      <p className="text-[13px] text-muted-foreground">{t("adminModels.caps.nativeHint")}</p>
      {rows.map((row, index) => {
        const issue = issues.find((i) => i.index === index)?.issue;
        return (
          <div key={index} className="space-y-2 rounded-lg border p-3">
            <div className="flex flex-wrap items-center gap-3">
              <span className="min-w-0 flex-1 truncate text-sm font-medium">{row.label || row.key || t("adminModels.caps.untitledTool")}</span>
              <label className="flex items-center gap-1.5 text-sm">
                <Switch checked={row.enabled} disabled={disabled} onCheckedChange={(on) => set(index, { enabled: on })} />
                {t("adminModels.caps.enabled")}
              </label>
              <label className="flex items-center gap-1.5 text-sm">
                <Switch checked={row.defaultEnabled} disabled={disabled} onCheckedChange={(on) => set(index, { defaultEnabled: on })} />
                {t("adminModels.caps.defaultEnabled")}
              </label>
              <Button type="button" variant="ghost" size="icon-sm" disabled={disabled} aria-label={t("adminCommon.common.remove")} onClick={() => onRows(rows.filter((_, i) => i !== index))}>
                <Trash2 className="text-destructive" />
              </Button>
            </div>
            <div className="grid gap-2 sm:grid-cols-2">
              <Labeled label={t("adminModels.caps.toolKey")}>
                <Input value={row.key} disabled={disabled} onChange={(e) => set(index, { key: e.target.value })} spellCheck={false} className="font-mono text-[12.5px]" />
              </Labeled>
              <Labeled label={t("adminModels.caps.toolLabel")}>
                <Input value={row.label} disabled={disabled} onChange={(e) => set(index, { label: e.target.value })} />
              </Labeled>
              <Labeled label={t("adminModels.caps.toolProtocols")}>
                <MultiSelectMenu values={row.protocols} options={protocolOptions} searchable disabled={disabled} label={t("adminModels.caps.toolProtocols")} placeholder={t("adminModels.caps.pickProtocols")} onChange={(values) => set(index, { protocols: values })} />
              </Labeled>
              <Labeled label={t("adminModels.caps.toolType")}>
                <Input value={row.type} disabled={disabled} onChange={(e) => set(index, { type: e.target.value })} spellCheck={false} className="font-mono text-[12.5px]" />
              </Labeled>
              <Labeled label={t("adminModels.caps.payload")} className="sm:col-span-2">
                <Textarea rows={4} value={row.payload} disabled={disabled} onChange={(e) => set(index, { payload: e.target.value })} spellCheck={false} className="font-mono text-[12.5px]" />
              </Labeled>
            </div>
            {issue ? <p className="text-[13px] text-destructive">{t(`adminModels.caps.nativeIssues.${issue}`, { row: index + 1 })}</p> : null}
          </div>
        );
      })}
      <div className="flex flex-wrap gap-2">
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <Button type="button" size="sm" variant="outline" disabled={disabled || catalog === null}>
              <Sparkles />
              {t("adminModels.caps.addFromCatalog")}
            </Button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start" className="max-h-72 overflow-y-auto">
            {offered.length === 0 ? <div className="px-2 py-3 text-sm text-muted-foreground">{t("adminModels.caps.catalogEmpty")}</div> : null}
            {offered.map((tool) => (
              <DropdownMenuItem key={`${tool.protocol}/${tool.toolKey}`} onSelect={() => onRows(addCatalogTool(rows, tool))}>
                <span className="flex-1">{tool.label}</span>
                <span className="text-xs text-muted-foreground">{tool.provider}</span>
              </DropdownMenuItem>
            ))}
          </DropdownMenuContent>
        </DropdownMenu>
        <Button type="button" size="sm" variant="outline" disabled={disabled} onClick={() => onRows([...rows, { key: "", protocols: routeProtocols.slice(0, 1), type: "", label: "", description: "", provider: "", enabled: true, defaultEnabled: false, payload: "{}" }])}>
          <Plus />
          {t("adminModels.caps.addCustomTool")}
        </Button>
      </div>
    </div>
  );
}

/**
 * The visual editor of the capabilities JSON plus the advanced text. Rows that
 * are still being typed live in this component and reach the text as they
 * become writable; `onProblem` reports the first validation failure.
 */
export function CapsEditor({ text, setText, resetKey, routeProtocols, onProblem, disabled, headerActions }: { text: string; resetKey: number; setText: (text: string) => void; routeProtocols: string[]; onProblem: (problem: ReturnType<typeof allIssues>) => void; disabled?: boolean; headerActions?: React.ReactNode }) {
  const t = useT();
  const doc = useCapsDoc(text, setText);
  const off = disabled || !doc.valid;
  const [controls, setControls] = useState<ControlRow[]>(() => controlRows(doc.caps));
  const [native, setNative] = useState<NativeToolRow[]>(() => nativeRows(doc.caps));

  // Rows are rebuilt from the text only when it was replaced as a whole (advanced box, presets, opening the form).
  const reload = (source: string) => {
    const parsed = parseCaps(source);
    if (!parsed.ok) return;
    setControls(controlRows(parsed.value));
    setNative(nativeRows(parsed.value));
  };
  useEffect(() => {
    reload(text);
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [resetKey]);

  useEffect(() => {
    onProblem(allIssues({ controls, native }));
  }, [controls, native, onProblem]);

  const writeThrough = (change: (caps: Record<string, unknown>) => Record<string, unknown>) => {
    doc.update(change);
  };

  const cache = readPromptCache(doc.caps);
  const stream = readImageStream(doc.caps);

  return (
    <div className="space-y-3">
      <div className="flex flex-wrap items-center justify-between gap-2">
        <h3 className="text-sm font-semibold">{t("adminModels.caps.title")}</h3>
        <div className="flex gap-2">{headerActions}</div>
      </div>
      {!doc.valid ? <p className="rounded-md border border-destructive/40 bg-destructive/5 p-2 text-[13px] text-destructive">{t("adminModels.caps.invalidJson")}</p> : null}

      <Collapsible title={t("adminModels.caps.controls")} summary={controls.length > 0 ? t("adminModels.caps.count", { count: controls.length }) : t("adminModels.caps.none")}>
        <ControlsEditor
          rows={controls}
          disabled={off}
          onRows={(rows) => {
            setControls(rows);
            writeThrough((c) => writeControls(c, rows));
          }}
        />
      </Collapsible>

      <Collapsible title={t("adminModels.caps.native")} summary={native.length > 0 ? t("adminModels.caps.count", { count: native.length }) : t("adminModels.caps.none")}>
        <NativeEditor
          rows={native}
          routeProtocols={routeProtocols}
          disabled={off}
          onRows={(rows) => {
            setNative(rows);
            writeThrough((c) => writeNative(c, rows));
          }}
        />
      </Collapsible>

      <Collapsible title={t("adminModels.caps.cache")} summary={t(`adminModels.caps.cacheModes.${cache.availability}`)}>
        <div className="space-y-3">
          {!cacheEditable(routeProtocols) ? <p className="text-[13px] text-muted-foreground">{t("adminModels.caps.cacheUnavailable")}</p> : null}
          <Labeled label={t("adminModels.caps.cacheAvailability")}>
            <NativeSelect
              value={cache.availability}
              disabled={off || !cacheEditable(routeProtocols)}
              aria-label={t("adminModels.caps.cacheAvailability")}
              onChange={(e) => doc.update((c) => writePromptCache(c, { ...cache, availability: e.target.value as CacheAvailability }))}
            >
              {(["auto", "enabled", "disabled"] as const).map((m) => (
                <option key={m} value={m}>
                  {t(`adminModels.caps.cacheModes.${m}`)}
                </option>
              ))}
            </NativeSelect>
          </Labeled>
          <label className="flex items-center gap-2 text-sm">
            <Switch checked={cache.explicit} disabled={off || !cacheEditable(routeProtocols) || cache.availability === "disabled"} onCheckedChange={(on) => doc.update((c) => writePromptCache(c, { ...cache, explicit: on }))} />
            {t("adminModels.caps.cacheExplicit")}
          </label>
        </div>
      </Collapsible>

      {imageToggleShown(routeProtocols) ? (
        <label className="flex items-center gap-2 rounded-lg border px-3 py-2.5 text-sm font-medium">
          <Switch checked={stream} disabled={off} onCheckedChange={(on) => doc.update((c) => writeImageStream(c, on))} />
          {t("adminModels.caps.imageStream")}
          <Badge tone="outline">{t(stream ? "adminCommon.common.on" : "adminCommon.common.off")}</Badge>
        </label>
      ) : null}

      <Collapsible title={t("adminModels.caps.advanced")}>
        <Textarea rows={10} value={text} disabled={disabled} onChange={(e) => {
            setText(e.target.value);
            reload(e.target.value);
          }} spellCheck={false} aria-label={t("adminModels.caps.advanced")} className="font-mono text-[12.5px]" />
        <p className="mt-2 text-[13px] text-muted-foreground">{t("adminModels.caps.advancedHint")}</p>
      </Collapsible>
    </div>
  );
}

