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
