import { type HTMLAttributes, type TdHTMLAttributes, type ThHTMLAttributes, type ReactNode } from "react";
import { cn } from "@/lib/utils/cn";

/** Scrolls horizontally on narrow screens; the header stays sticky inside `maxHeight`. */
export function Table({ className, containerClassName, ...props }: HTMLAttributes<HTMLTableElement> & { containerClassName?: string }) {
  return (
    <div className={cn("relative w-full overflow-auto rounded-lg border", containerClassName)}>
      <table className={cn("w-full caption-bottom border-collapse text-sm", className)} {...props} />
    </div>
  );
}

export function TableHeader({ className, ...props }: HTMLAttributes<HTMLTableSectionElement>) {
  return <thead className={cn("sticky top-0 z-10 bg-muted/80 backdrop-blur [&_tr]:border-b", className)} {...props} />;
}

export function TableBody({ className, ...props }: HTMLAttributes<HTMLTableSectionElement>) {
  return <tbody className={cn("[&_tr:last-child]:border-0", className)} {...props} />;
}

export function TableRow({
  className,
  tone,
  ...props
}: HTMLAttributes<HTMLTableRowElement> & { tone?: "muted" | "warning" }) {
  return (
    <tr
      className={cn(
        "border-b transition-colors hover:bg-muted/50 data-[state=selected]:bg-accent",
        tone === "muted" && "bg-muted/40 text-muted-foreground",
        tone === "warning" && "bg-warning/10",
        className,
      )}
      {...props}
    />
  );
}

export function TableHead({ className, ...props }: ThHTMLAttributes<HTMLTableCellElement>) {
  return (
    <th
      className={cn("h-10 whitespace-nowrap px-3 text-left align-middle text-xs font-semibold uppercase tracking-wide text-muted-foreground", className)}
      {...props}
    />
  );
}

export function TableCell({ className, ...props }: TdHTMLAttributes<HTMLTableCellElement>) {
  return <td className={cn("px-3 py-2.5 align-middle", className)} {...props} />;
}

/** A row that spans every column, for "nothing here" and "loading" states. */
export function TableEmptyRow({ colSpan, children }: { colSpan: number; children: ReactNode }) {
  return (
    <tr>
      <td colSpan={colSpan} className="h-28 px-3 text-center text-sm text-muted-foreground">
        {children}
      </td>
    </tr>
  );
}
