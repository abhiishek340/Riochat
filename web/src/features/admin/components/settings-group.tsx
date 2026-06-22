"use client";

import type { ReactNode } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Input, Textarea } from "@/components/ui/input";
import { Skeleton } from "@/components/ui/skeleton";
import { Switch } from "@/components/ui/switch";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { boolOf, fieldId, isVisible, setBool, type FieldDef, type FieldOption } from "../lib/settings-form";
import type { SaveOptions, SettingsForm } from "../lib/use-settings-form";
import { Checkbox } from "./checkbox";
import { LoadError } from "./load-error";
import { Section } from "./page";
import { OptionSelect } from "./toolbar";

function optionLabel(option: FieldOption, t: ReturnType<typeof useT>): string {
  return option.labelKey ? t(option.labelKey) : (option.text ?? option.value);
}

/** Options of a select; a stored value that is no longer offered stays visible as itself. */
function selectOptions(field: FieldDef, current: string, t: ReturnType<typeof useT>) {
  const list = (field.options ?? []).map((o) => ({ value: o.value, label: optionLabel(o, t) }));
  if (current !== "" && !list.some((o) => o.value === current)) list.push({ value: current, label: current });
  return list;
}

/** Comma-separated set stored in one setting. */
function csv(value: string): string[] {
  return value
    .split(",")
    .map((x) => x.trim())
    .filter(Boolean);
}

export interface FieldControlProps {
  field: FieldDef;
  form: SettingsForm;
  disabled?: boolean;
  onChange?: (id: string, value: string) => void;
}

/** Renders the control of one setting; it does not draw the label. */
export function FieldControl({ field, form, disabled, onChange }: FieldControlProps) {
  const t = useT();
  const id = fieldId(field);
  const value = form.draft[id] ?? "";
  const configured = form.configured.has(id);
  const set = (next: string) => (onChange ?? form.set)(id, next);
  const off = disabled || form.saving || form.loading;
  const inputId = `f-${id}`;
  switch (field.type) {
    case "bool":
      return <Switch id={inputId} checked={boolOf(form.draft, id)} disabled={off} onCheckedChange={(on) => set(setBool({}, id, on)[id] as string)} aria-label={t(field.label)} />;
    case "textarea":
    case "json":
      return (
        <Textarea
          id={inputId}
          value={value}
          disabled={off}
          rows={field.type === "json" ? 8 : 4}
          spellCheck={false}
          placeholder={field.placeholder}
          onChange={(event) => set(event.target.value)}
          className={cn("w-full", field.type === "json" && "font-mono text-[12.5px]")}
        />
      );
    case "select":
      return (
        <OptionSelect
          value={value}
          disabled={off}
          label={t(field.label)}
          onChange={set}
          options={selectOptions(field, value, t)}
          className="sm:w-56"
        />
      );
    case "tabs":
      return (
        <div role="radiogroup" aria-label={t(field.label)} className="inline-flex rounded-lg bg-muted p-1">
          {(field.options ?? []).map((o) => (
            <button
              key={o.value}
              role="radio"
              type="button"
              aria-checked={value === o.value}
              disabled={off}
              onClick={() => set(o.value)}
              className={cn("rounded-md px-3 py-1 text-sm font-medium transition-colors disabled:opacity-50", value === o.value ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground")}
            >
              {optionLabel(o, t)}
            </button>
          ))}
        </div>
      );
    case "multi": {
      const chosen = new Set(csv(value));
      return (
        <div className="flex flex-wrap gap-x-4 gap-y-2">
          {(field.options ?? []).map((o) => (
            <label key={o.value} className="flex items-center gap-2 text-sm">
              <Checkbox
                checked={chosen.has(o.value)}
                disabled={off}
                onCheckedChange={(on) => {
                  const next = new Set(chosen);
                  if (on) next.add(o.value);
                  else next.delete(o.value);
                  // Keep the options' own order so equal sets compare equal.
                  set((field.options ?? []).filter((x) => next.has(x.value)).map((x) => x.value).join(","));
                }}
              />
              {optionLabel(o, t)}
            </label>
          ))}
        </div>
      );
    }
    case "password":
      return (
        <Input
          id={inputId}
          type="password"
          autoComplete="new-password"
          value={value}
          disabled={off}
          placeholder={configured ? t("adminCommon.settings.configured") : field.placeholder}
          onChange={(event) => set(event.target.value)}
          className="sm:w-72"
        />
      );
    case "int":
      return (
        <div className="flex items-center gap-2">
          <Input
            id={inputId}
            inputMode={field.unit === "mb" ? "decimal" : "numeric"}
            value={value}
            disabled={off}
            placeholder={field.placeholder}
            onChange={(event) => set(event.target.value)}
            className="w-32 text-right tabular-nums"
          />
          {field.suffix ? <span className="text-sm text-muted-foreground">{field.suffix}</span> : null}
        </div>
      );
    default:
      return (
        <Input
          id={inputId}
          value={value}
          disabled={off}
          placeholder={configured && field.secret ? t("adminCommon.settings.configured") : field.placeholder}
          onChange={(event) => set(event.target.value)}
          className="sm:w-72"
        />
      );
  }
}
