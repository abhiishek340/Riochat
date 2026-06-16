"use client";

import { Check, ChevronsUpDown, Search } from "lucide-react";
import { useMemo, useState, type ReactNode } from "react";
import { useFieldControl } from "@/components/ui/field";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger } from "@/components/ui/dropdown-menu";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";

export interface MenuOption {
  value: string;
  label: ReactNode;
  /** Plain text used for searching and for the trigger summary. */
  text?: string;
  hint?: ReactNode;
  disabled?: boolean;
}

export function optionText(option: MenuOption): string {
  return option.text ?? (typeof option.label === "string" ? option.label : option.value);
}

export function filterOptions(options: readonly MenuOption[], query: string): MenuOption[] {
  const q = query.trim().toLowerCase();
  if (!q) return [...options];
  return options.filter((o) => optionText(o).toLowerCase().includes(q) || o.value.toLowerCase().includes(q));
}

const triggerClass =
  "flex h-9 w-full min-w-0 items-center justify-between gap-2 rounded-md border border-input bg-background px-3 text-left text-sm shadow-sm outline-none transition-[border-color,box-shadow] focus-visible:border-ring focus-visible:ring-2 focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50";

function SearchField({ value, onChange }: { value: string; onChange: (value: string) => void }) {
  const t = useT();
  return (
    <div className="relative mb-1 px-1 pt-1">
      <Search aria-hidden className="pointer-events-none absolute left-3 top-1/2 size-3.5 -translate-y-1/2 text-muted-foreground" />
      <input
        autoFocus
        value={value}
        onChange={(event) => onChange(event.target.value)}
        // Keep the menu's own type-ahead and arrow handling out of the text field.
        onKeyDown={(event) => {
          if (event.key !== "Escape" && event.key !== "ArrowDown" && event.key !== "ArrowUp") event.stopPropagation();
        }}
        placeholder={t("common.select.searchPlaceholder")}
        aria-label={t("common.select.searchPlaceholder")}
        className="h-8 w-full rounded-md border border-input bg-background pl-7 pr-2 text-sm outline-none focus-visible:ring-2 focus-visible:ring-ring/40"
      />
    </div>
  );
}

interface BaseProps {
  options: readonly MenuOption[];
  placeholder?: string;
  label?: string;
  searchable?: boolean;
  disabled?: boolean;
  className?: string;
  contentClassName?: string;
  emptyText?: string;
}

/** Single choice from a list; the trigger shows the chosen label. */
export function SelectMenu({ value, onChange, ...rest }: BaseProps & { value: string; onChange: (value: string) => void }) {
  const t = useT();
  const { options, placeholder, label, searchable, disabled, className, contentClassName, emptyText } = rest;
  const [open, setOpen] = useState(false);
  const [query, setQuery] = useState("");
  const shown = useMemo(() => filterOptions(options, query), [options, query]);
  const current = options.find((o) => o.value === value);
  const field = useFieldControl<{ id?: string; "aria-invalid"?: boolean; "aria-describedby"?: string }>({});
  const named = label ? (current ? `${label}: ${optionText(current)}` : label) : undefined;
  return (
    <DropdownMenu
      open={open}
      onOpenChange={(next) => {
        setOpen(next);
        if (!next) setQuery("");
      }}
    >
      <DropdownMenuTrigger disabled={disabled} aria-label={named} {...(field.id ? { id: field.id } : {})} aria-invalid={field["aria-invalid"]} aria-describedby={field["aria-describedby"]} className={cn(triggerClass, className)}>
        <span className={cn("truncate", !current && "text-muted-foreground")}>{current ? current.label : (placeholder ?? t("common.select.placeholder"))}</span>
        <ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className={cn("max-h-72 min-w-[var(--radix-dropdown-menu-trigger-width)] overflow-y-auto", contentClassName)}>
        {searchable ? <SearchField value={query} onChange={setQuery} /> : null}
        {shown.length === 0 ? <div className="px-2 py-3 text-center text-sm text-muted-foreground">{emptyText ?? t("common.select.empty")}</div> : null}
        {shown.map((option) => (
          <DropdownMenuItem key={option.value} role="menuitemradio" aria-checked={option.value === value} disabled={option.disabled} onSelect={() => onChange(option.value)} className="justify-between">
            <span className="min-w-0 flex-1 truncate">{option.label}</span>
            {option.hint ? <span className="text-xs text-muted-foreground">{option.hint}</span> : null}
            {option.value === value ? <Check className="!text-foreground" /> : null}
          </DropdownMenuItem>
        ))}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}

/** Several choices from a list; the menu stays open while ticking. */
export function MultiSelectMenu({ values, onChange, max, ...rest }: BaseProps & { values: readonly string[]; onChange: (values: string[]) => void; max?: number }) {
  const t = useT();
  const { options, placeholder, label, searchable, disabled, className, contentClassName, emptyText } = rest;
  const [query, setQuery] = useState("");
  const shown = useMemo(() => filterOptions(options, query), [options, query]);
  const chosen = new Set(values);
  const summary = options.filter((o) => chosen.has(o.value)).map(optionText);
  const extra = values.filter((v) => !options.some((o) => o.value === v));
  const text = [...summary, ...extra].join(", ");
  const field = useFieldControl<{ id?: string; "aria-invalid"?: boolean; "aria-describedby"?: string }>({});
  const named = label ? (text ? `${label}: ${text}` : label) : undefined;
  return (
    <DropdownMenu onOpenChange={(open) => !open && setQuery("")}>
      <DropdownMenuTrigger disabled={disabled} aria-label={named} {...(field.id ? { id: field.id } : {})} aria-invalid={field["aria-invalid"]} aria-describedby={field["aria-describedby"]} className={cn(triggerClass, className)}>
        <span className={cn("truncate", !text && "text-muted-foreground")}>{text || placeholder || t("common.select.placeholder")}</span>
        <ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" />
      </DropdownMenuTrigger>
      <DropdownMenuContent align="start" className={cn("max-h-72 min-w-[var(--radix-dropdown-menu-trigger-width)] overflow-y-auto", contentClassName)}>
        {searchable ? <SearchField value={query} onChange={setQuery} /> : null}
        {shown.length === 0 ? <div className="px-2 py-3 text-center text-sm text-muted-foreground">{emptyText ?? t("common.select.empty")}</div> : null}
        {shown.map((option) => {
          const on = chosen.has(option.value);
          return (
            <DropdownMenuItem
              key={option.value}
              role="menuitemcheckbox"
              aria-checked={on}
              disabled={option.disabled || (!on && max !== undefined && values.length >= max)}
              onSelect={(event) => {
                event.preventDefault();
                onChange(on ? values.filter((v) => v !== option.value) : [...values, option.value]);
              }}
              className="justify-between"
            >
              <span className="min-w-0 flex-1 truncate">{option.label}</span>
              {option.hint ? <span className="text-xs text-muted-foreground">{option.hint}</span> : null}
              <span className={cn("flex size-4 items-center justify-center rounded border", on ? "border-primary bg-primary text-primary-foreground" : "border-input")}>
                {on ? <Check className="!size-3 !text-primary-foreground" /> : null}
              </span>
            </DropdownMenuItem>
          );
        })}
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
