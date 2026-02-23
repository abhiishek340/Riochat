import { Loader2 } from "lucide-react";
import { cn } from "@/lib/utils/cn";

export function Spinner({ className, label }: { className?: string; label?: string }) {
  return (
    <Loader2
      aria-hidden={label ? undefined : true}
      aria-label={label}
      className={cn("size-4 animate-[rio-spin_0.9s_linear_infinite]", className)}
    />
  );
}
