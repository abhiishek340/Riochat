import type { ReactNode } from "react";
import { cn } from "@/lib/utils/cn";

export type DetailItem = readonly [label: ReactNode, value: ReactNode] | null | false | undefined;

/** Label/value pairs for detail sheets; empty values show a dash. */
export function DetailList({ items, className }: { items: readonly DetailItem[]; className?: string }) {
  return (
    <dl className={cn("grid grid-cols-[minmax(7rem,10rem)_1fr] gap-x-4 gap-y-2 text-sm", className)}>
      {items.map((item, i) =>
        item ? (
          <div key={i} className="contents">
            <dt className="text-muted-foreground">{item[0]}</dt>
            <dd className="min-w-0 break-words">{item[1] === "" || item[1] === null || item[1] === undefined ? "-" : item[1]}</dd>
          </div>
        ) : null,
      )}
    </dl>
  );
}

export function DetailSection({ title, children }: { title: ReactNode; children: ReactNode }) {
  return (
    <section className="space-y-2">
      <h3 className="text-sm font-semibold">{title}</h3>
      {children}
    </section>
  );
}
