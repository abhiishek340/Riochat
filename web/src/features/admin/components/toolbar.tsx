"use client";

import { Search, X } from "lucide-react";
import { useEffect, useRef, useState, type ReactNode } from "react";
import { Input, NativeSelect } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";

export function Toolbar({ children, className }: { children: ReactNode; className?: string }) {
  return <div className={cn("flex flex-wrap items-center gap-2", className)}>{children}</div>;
}

/** Search field that reports its value 250 ms after typing stops. */
export function SearchBox({ value, onChange, placeholder, delay = 250, className }: { value: string; onChange: (value: string) => void; placeholder?: string; delay?: number; className?: string }) {
  const t = useT();
  const [text, setText] = useState(value);
  const onChangeRef = useRef(onChange);
  onChangeRef.current = onChange;
  const latestValue = useRef(value);
  latestValue.current = value;

  // Follow outside resets (for example "clear filters").
  useEffect(() => {
    setText((current) => (current.trim() === value.trim() ? current : value));
  }, [value]);

  useEffect(() => {
    if (text === latestValue.current) return;
    const id = setTimeout(() => onChangeRef.current(text), delay);
    return () => clearTimeout(id);
  }, [text, delay]);

  return (
    <div className={cn("relative w-full sm:w-64", className)}>
      <Search aria-hidden className="pointer-events-none absolute left-2.5 top-1/2 size-4 -translate-y-1/2 text-muted-foreground" />
      <Input
        value={text}
        onChange={(event) => setText(event.target.value)}
        placeholder={placeholder ?? t("adminCommon.table.searchPlaceholder")}
        aria-label={placeholder ?? t("adminCommon.table.searchPlaceholder")}
        className="pl-8 pr-8"
      />
      {text ? (
        <button
          type="button"
          aria-label={t("common.actions.reset")}
          onClick={() => {
            setText("");
            onChangeRef.current("");
          }}
          className="absolute right-1.5 top-1/2 inline-flex size-6 -translate-y-1/2 items-center justify-center rounded text-muted-foreground hover:bg-accent"
        >
          <X className="size-3.5" />
        </button>
      ) : null}
    </div>
  );
}

export interface SelectOption {
  value: string;
  label: string;
  disabled?: boolean;
}

/** A native select driven by an option list. */
export function OptionSelect({ value, onChange, options, label, className, disabled, placeholder }: { value: string; onChange: (value: string) => void; options: readonly SelectOption[]; label?: string; className?: string; disabled?: boolean; placeholder?: string }) {
  return (
    <NativeSelect value={value} onChange={(event) => onChange(event.target.value)} aria-label={label} disabled={disabled} className={cn("w-full sm:w-auto sm:min-w-32", className)}>
      {placeholder !== undefined ? <option value="">{placeholder}</option> : null}
      {options.map((option) => (
        <option key={option.value} value={option.value} disabled={option.disabled}>
          {option.label}
        </option>
      ))}
    </NativeSelect>
  );
}
