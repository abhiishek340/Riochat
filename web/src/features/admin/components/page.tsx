"use client";

import type { ReactNode } from "react";
import { Card } from "@/components/ui/card";
import { cn } from "@/lib/utils/cn";

/** Standard frame of an admin screen. */
export function AdminPage({ title, description, actions, children, className }: { title: ReactNode; description?: ReactNode; actions?: ReactNode; children: ReactNode; className?: string }) {
  return (
    <div className={cn("mx-auto w-full max-w-7xl space-y-6 p-4 sm:p-6", className)}>
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="min-w-0 space-y-1">
          {/* The shell header owns the page's single h1; the page title is the next level down. */}
          <h2 className="text-xl font-semibold tracking-tight">{title}</h2>
          {description ? <p className="max-w-3xl text-sm text-muted-foreground">{description}</p> : null}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </header>
      {children}
    </div>
  );
}

/** A titled card; `flush` removes the inner padding for tables. */
export function Section({ title, description, actions, children, flush = false, className }: { title?: ReactNode; description?: ReactNode; actions?: ReactNode; children: ReactNode; flush?: boolean; className?: string }) {
  return (
    <Card className={cn("overflow-hidden", className)}>
      {title || actions ? (
        <div className="flex flex-wrap items-start justify-between gap-3 border-b px-5 py-3.5">
          <div className="min-w-0">
            {title ? <h3 className="text-[15px] font-semibold leading-tight">{title}</h3> : null}
            {description ? <p className="mt-1 text-[13px] text-muted-foreground">{description}</p> : null}
          </div>
          {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
        </div>
      ) : null}
      <div className={flush ? "" : "p-5"}>{children}</div>
    </Card>
  );
}

/** Label/description on the left, control on the right. */
export function SettingRow({ label, hint, children, htmlFor, className }: { label: ReactNode; hint?: ReactNode; children: ReactNode; htmlFor?: string; className?: string }) {
  return (
    <div className={cn("flex flex-col gap-2 py-3 sm:flex-row sm:items-center sm:justify-between sm:gap-6", className)}>
      <div className="min-w-0 sm:max-w-[55%]">
        <label htmlFor={htmlFor} className="text-sm font-medium">
          {label}
        </label>
        {hint ? <p className="mt-0.5 text-[13px] text-muted-foreground">{hint}</p> : null}
      </div>
      <div className="flex min-w-0 shrink-0 items-center gap-2 sm:justify-end">{children}</div>
    </div>
  );
}

export function Divider({ className }: { className?: string }) {
  return <hr className={cn("border-border", className)} />;
}

export function Mono({ children, className }: { children: ReactNode; className?: string }) {
  return <span className={cn("font-mono text-[12.5px]", className)}>{children}</span>;
}
