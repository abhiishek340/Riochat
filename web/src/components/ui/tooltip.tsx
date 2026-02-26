"use client";

import { type ComponentPropsWithoutRef, type ReactNode } from "react";
import * as Primitive from "@radix-ui/react-tooltip";
import { cn } from "@/lib/utils/cn";

export function TooltipProvider({ children }: { children: ReactNode }) {
  return (
    <Primitive.Provider delayDuration={350} skipDelayDuration={200}>
      {children}
    </Primitive.Provider>
  );
}

/**
 * Convenience wrapper: `<Tooltip label="Copy"><Button .../></Tooltip>`.
 * With an empty label the child renders alone.
 */
export function Tooltip({
  label,
  children,
  side = "top",
  className,
  ...props
}: Omit<ComponentPropsWithoutRef<typeof Primitive.Content>, "children"> & { label: ReactNode; children: ReactNode }) {
  if (!label) return <>{children}</>;
  return (
    <Primitive.Root>
      <Primitive.Trigger asChild>{children}</Primitive.Trigger>
      <Primitive.Portal>
        <Primitive.Content
          side={side}
          sideOffset={6}
          className={cn(
            "z-50 max-w-64 rounded-md bg-foreground px-2.5 py-1.5 text-xs font-medium text-background shadow-md data-[state=delayed-open]:animate-[rio-fade-in_120ms_ease-out] data-[state=instant-open]:animate-[rio-fade-in_80ms_ease-out]",
            className,
          )}
          {...props}
        >
          {label}
        </Primitive.Content>
      </Primitive.Portal>
    </Primitive.Root>
  );
}
