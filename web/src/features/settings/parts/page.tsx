import type { HTMLAttributes, ReactNode } from "react";
import { cn } from "@/lib/utils/cn";

/** Page frame: heading, optional blurb and actions, then stacked sections. */
export function SettingsPage({
  title,
  description,
  actions,
  children,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  children: ReactNode;
  className?: string;
}) {
  return (
    <div className={cn("grid gap-8", className)}>
      <header className="flex flex-wrap items-start justify-between gap-3">
        <div className="grid min-w-0 gap-1">
          <h2 className="text-xl font-semibold tracking-tight">{title}</h2>
          {description ? <p className="max-w-2xl text-sm text-muted-foreground">{description}</p> : null}
        </div>
        {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
      </header>
      {children}
    </div>
  );
}

/** A titled group. Its body is a bordered panel unless `bare` is set. */
export function SettingsSection({
  title,
  description,
  actions,
  bare = false,
  children,
  className,
  ...rest
}: {
  title?: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  bare?: boolean;
  children: ReactNode;
} & Omit<HTMLAttributes<HTMLElement>, "title">) {
  return (
    <section className={cn("grid gap-3", className)} {...rest}>
      {title || actions ? (
        <div className="flex flex-wrap items-end justify-between gap-2">
          <div className="grid gap-0.5">
            {title ? <h3 className="text-sm font-semibold uppercase tracking-wide text-muted-foreground">{title}</h3> : null}
            {description ? <p className="text-sm text-muted-foreground">{description}</p> : null}
          </div>
          {actions ? <div className="flex flex-wrap items-center gap-2">{actions}</div> : null}
        </div>
      ) : null}
      {bare ? children : <div className="divide-y rounded-xl border bg-card shadow-sm">{children}</div>}
    </section>
  );
}

/** One labelled control inside a panel. Stacks on narrow screens. */
export function SettingRow({
  title,
  description,
  children,
  htmlFor,
  align = "center",
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  children?: ReactNode;
  htmlFor?: string;
  align?: "center" | "start";
  className?: string;
}) {
  return (
    <div
      className={cn(
        "flex flex-col gap-3 px-4 py-3.5 sm:flex-row sm:justify-between sm:gap-6",
        align === "center" ? "sm:items-center" : "sm:items-start",
        className,
      )}
    >
      <div className="grid min-w-0 flex-1 gap-0.5">
        {htmlFor ? (
          <label htmlFor={htmlFor} className="text-sm font-medium leading-snug">
            {title}
          </label>
        ) : (
          <span className="text-sm font-medium leading-snug">{title}</span>
        )}
        {description ? <p className="text-[13px] leading-snug text-muted-foreground">{description}</p> : null}
      </div>
      {children ? <div className="flex shrink-0 flex-wrap items-center gap-2 sm:justify-end">{children}</div> : null}
    </div>
  );
}

/** A panel row holding free-form content. */
export function PanelBody({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("p-4", className)} {...props} />;
}
