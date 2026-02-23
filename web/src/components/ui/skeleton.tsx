import { type HTMLAttributes } from "react";
import { cn } from "@/lib/utils/cn";

export function Skeleton({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div aria-hidden className={cn("animate-[rio-pulse_1.6s_ease-in-out_infinite] rounded-md bg-muted", className)} {...props} />;
}
