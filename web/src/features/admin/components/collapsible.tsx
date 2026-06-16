"use client";

import { ChevronRight } from "lucide-react";
import { useId, useState, type ReactNode } from "react";
import { cn } from "@/lib/utils/cn";

/** A titled block that opens and closes. */
export function Collapsible({ title, summary, defaultOpen = false, children, className }: { title: ReactNode; summary?: ReactNode; defaultOpen?: boolean; children: ReactNode; className?: string }) {
  const [open, setOpen] = useState(defaultOpen);
  const id = useId();
  return (
    <div className={cn("rounded-lg border", className)}>
      <button type="button" aria-expanded={open} aria-controls={id} onClick={() => setOpen((v) => !v)} className="flex w-full items-center gap-2 px-3 py-2.5 text-left text-sm font-medium">
        <ChevronRight className={cn("size-4 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")} />
        <span>{title}</span>
        {summary ? <span className="ml-auto truncate text-xs font-normal text-muted-foreground">{summary}</span> : null}
      </button>
      {open ? (
        <div id={id} className="border-t px-3 py-3">
          {children}
        </div>
      ) : null}
    </div>
  );
}
