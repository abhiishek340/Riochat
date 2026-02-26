"use client";

import { type ComponentPropsWithoutRef } from "react";
import * as Primitive from "@radix-ui/react-popover";
import { cn } from "@/lib/utils/cn";

export const Popover = Primitive.Root;
export const PopoverTrigger = Primitive.Trigger;
export const PopoverAnchor = Primitive.Anchor;
export const PopoverClose = Primitive.Close;

export function PopoverContent({
  className,
  align = "start",
  sideOffset = 8,
  collisionPadding = 12,
  ...props
}: ComponentPropsWithoutRef<typeof Primitive.Content>) {
  return (
    <Primitive.Portal>
      <Primitive.Content
        align={align}
        sideOffset={sideOffset}
        collisionPadding={collisionPadding}
        className={cn(
          "z-50 max-h-[var(--radix-popover-content-available-height)] overflow-auto rounded-xl border bg-popover p-1.5 text-popover-foreground shadow-lg outline-none",
          "data-[state=open]:animate-[rio-pop-in_150ms_cubic-bezier(0.22,1,0.36,1)] data-[state=closed]:animate-[rio-pop-out_100ms_ease-in]",
          className,
        )}
        {...props}
      />
    </Primitive.Portal>
  );
}
