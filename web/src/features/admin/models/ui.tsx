"use client";

import { useId, type ReactNode } from "react";
import { cn } from "@/lib/utils/cn";

/**
 * A caption above a control. With one control inside, the caption is a real
 * label for it (clicking it focuses the control and it becomes the control's
 * name). With several controls (`group`) the caption names the group.
 */
export function Labeled({ label, hint, error, children, className, group = false }: { label: ReactNode; hint?: ReactNode; error?: ReactNode; children: ReactNode; className?: string; group?: boolean }) {
  const id = useId();
  const caption = (
    <span id={id} className="text-sm font-medium">
      {label}
    </span>
  );
  return (
    <div className={cn("grid min-w-0 gap-1.5", className)}>
      {group ? (
        <div role="group" aria-labelledby={id} className="grid gap-1.5">
          {caption}
          {children}
        </div>
      ) : (
        <label className="grid gap-1.5">
          {caption}
          {children}
        </label>
      )}
      {error ? <p className="text-[13px] text-destructive">{error}</p> : null}
      {hint ? <p className="text-[13px] text-muted-foreground">{hint}</p> : null}
    </div>
  );
}
