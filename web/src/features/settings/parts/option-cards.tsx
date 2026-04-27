"use client";

import type { ReactNode } from "react";
import { cn } from "@/lib/utils/cn";

/** A grid of mutually exclusive, pressable preview cards. */
export function OptionGrid({
  label,
  columns = "sm:grid-cols-4",
  children,
  className,
}: {
  label: string;
  columns?: string;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div role="group" aria-label={label} className={cn("grid grid-cols-2 gap-2.5", columns, className)}>
      {children}
    </div>
  );
}

export function OptionCard({
  pressed,
  onPress,
  disabled,
  title,
  caption,
  children,
  className,
}: {
  pressed: boolean;
  onPress: () => void;
  disabled?: boolean;
  title: ReactNode;
  caption?: ReactNode;
  children?: ReactNode;
  className?: string;
}) {
  return (
    <button
      type="button"
      aria-pressed={pressed}
      disabled={disabled}
      onClick={onPress}
      className={cn(
        "group grid min-w-0 gap-2 rounded-lg border bg-background p-2.5 text-left outline-none transition-[border-color,box-shadow,background-color] hover:bg-accent/40 focus-visible:ring-2 focus-visible:ring-ring disabled:cursor-not-allowed disabled:opacity-50",
        pressed && "border-primary bg-accent/50 shadow-[0_0_0_1px_var(--color-primary)]",
        className,
      )}
    >
      {children ? <div className="overflow-hidden rounded-md">{children}</div> : null}
      <div className="flex min-w-0 items-baseline justify-between gap-2">
        <span className="truncate text-sm font-medium">{title}</span>
        {caption ? <span className="shrink-0 text-xs text-muted-foreground">{caption}</span> : null}
      </div>
    </button>
  );
}
