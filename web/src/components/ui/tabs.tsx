"use client";

import { type ComponentPropsWithoutRef } from "react";
import * as Primitive from "@radix-ui/react-tabs";
import { cn } from "@/lib/utils/cn";

export const Tabs = Primitive.Root;

export function TabsList({ className, ...props }: ComponentPropsWithoutRef<typeof Primitive.List>) {
  return (
    <Primitive.List
      className={cn("inline-flex h-9 items-center gap-1 rounded-lg bg-muted p-1 text-muted-foreground", className)}
      {...props}
    />
  );
}

export function TabsTrigger({ className, ...props }: ComponentPropsWithoutRef<typeof Primitive.Trigger>) {
  return (
    <Primitive.Trigger
      className={cn(
        "inline-flex items-center justify-center gap-1.5 whitespace-nowrap rounded-md px-3 py-1 text-sm font-medium outline-none transition-[background-color,color,box-shadow] focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-50 data-[state=active]:bg-background data-[state=active]:text-foreground data-[state=active]:shadow-sm",
        className,
      )}
      {...props}
    />
  );
}

export function TabsContent({ className, ...props }: ComponentPropsWithoutRef<typeof Primitive.Content>) {
  return <Primitive.Content className={cn("mt-4 outline-none focus-visible:ring-2 focus-visible:ring-ring", className)} {...props} />;
}
