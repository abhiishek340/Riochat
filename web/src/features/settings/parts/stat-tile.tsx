import type { ReactNode } from "react";
import { Skeleton } from "@/components/ui/skeleton";
import { cn } from "@/lib/utils/cn";

export function StatTile({
  label,
  value,
  hint,
  loading,
  className,
}: {
  label: ReactNode;
  value: ReactNode;
  hint?: ReactNode;
  loading?: boolean;
  className?: string;
}) {
  return (
    <div className={cn("grid gap-1 rounded-lg border bg-background px-3.5 py-3", className)}>
      <span className="text-xs text-muted-foreground">{label}</span>
      {loading ? <Skeleton className="h-6 w-20" /> : <span className="text-lg font-semibold tabular-nums leading-tight">{value}</span>}
      {hint ? <span className="text-xs text-muted-foreground">{hint}</span> : null}
    </div>
  );
}
