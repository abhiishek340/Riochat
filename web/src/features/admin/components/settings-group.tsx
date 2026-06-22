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

/** Label, hint, unsaved badge and control for one setting. */
export function FieldRow({ field, form, disabled, onChange }: FieldControlProps) {
  const t = useT();
  const id = fieldId(field);
  const dirty = form.dirtyOf([field]).length > 0;
  const wide = field.type === "textarea" || field.type === "json";
  const hint = field.hint ? t(field.hint) : "";
  const label = (
    <div className="min-w-0">
      <label htmlFor={`f-${id}`} className="flex flex-wrap items-center gap-2 text-sm font-medium">
        {t(field.label)}
        {dirty ? <Badge tone="warning">{t("adminCommon.settings.unsaved")}</Badge> : null}
      </label>
      {hint ? <p className="mt-0.5 text-[13px] text-muted-foreground">{hint}</p> : null}
    </div>
  );
  if (wide) {
    return (
      <div className="space-y-2 py-3">
        {label}
        <FieldControl field={field} form={form} disabled={disabled} onChange={onChange} />
      </div>
    );
  }
  return (
    <div className="flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-6">
      <div className="sm:max-w-[55%]">{label}</div>
      <div className="flex min-w-0 shrink-0 items-center gap-2 sm:justify-end">
        <FieldControl field={field} form={form} disabled={disabled} onChange={onChange} />
      </div>
    </div>
  );
}

export interface SettingsGroupProps {
  form: SettingsForm;
  fields: readonly FieldDef[];
  title?: ReactNode;
  description?: ReactNode;
  save?: SaveOptions;
  /** Called after a successful save with the fields that were sent. */
  onSaved?: (sent: FieldDef[]) => void;
  onChange?: (id: string, value: string) => void;
  /** Extra content shown under the fields (probes, hints). */
  children?: ReactNode;
  /** Rendered between the title and the fields. */
  before?: ReactNode;
  actions?: ReactNode;
  disabled?: boolean;
  /** Disables single fields, for example while a parent switch is off. */
  fieldDisabled?: (field: FieldDef, draft: Record<string, string>) => boolean;
  className?: string;
}

/** A card of settings with its own save bar, shown only while something changed. */
export function SettingsGroup({ form, fields, title, description, save, onSaved, onChange, children, before, actions, disabled, fieldDisabled, className }: SettingsGroupProps) {
  const t = useT();
  const visible = fields.filter((f) => isVisible(f, form.draft));
  const dirty = form.dirtyOf(fields);
  return (
    <Section title={title} description={description} actions={actions} className={className}>
      {before}
      {form.failed && !form.loading ? (
        // Never offer blank fields after a failed read: saving them would overwrite the server's values.
        <LoadError message={t("adminCommon.settings.loadFailed")} onRetry={form.reload} />
      ) : form.loading ? (
        <div className="space-y-3 py-2">
          {Array.from({ length: Math.min(4, Math.max(2, fields.length)) }, (_, i) => (
            <Skeleton key={i} className="h-9 w-full" />
          ))}
        </div>
      ) : (
        <div className="divide-y">
          {visible.map((f) => (
            <FieldRow key={fieldId(f)} field={f} form={form} disabled={disabled || fieldDisabled?.(f, form.draft)} onChange={onChange} />
          ))}
        </div>
      )}
      {form.failed && !form.loading ? null : children}
      {dirty.length > 0 ? (
        <div className="mt-4 flex flex-wrap items-center justify-end gap-2 border-t pt-4">
          <span className="mr-auto text-sm text-muted-foreground">{t("adminCommon.settings.unsaved")}</span>
          <Button variant="outline" disabled={form.saving} onClick={() => form.revert(fields)}>
            {t("adminCommon.settings.revert")}
          </Button>
          <Button
            loading={form.saving}
            onClick={async () => {
              const result = await form.save(fields, save);
              if (result.ok && result.sent.length > 0) onSaved?.(result.sent);
            }}
          >
            {t("adminCommon.settings.save")}
          </Button>
        </div>
      ) : null}
    </Section>
  );
}
