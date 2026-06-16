"use client";

import * as DialogPrimitive from "@radix-ui/react-dialog";
import { X } from "lucide-react";
import type { ReactNode } from "react";
import { DialogOverlay, DialogPortal } from "@/components/ui/dialog";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";

const SIZES = {
  sm: "sm:max-w-md",
  md: "sm:max-w-xl",
  lg: "sm:max-w-3xl",
  xl: "sm:max-w-5xl",
} as const;

export interface SheetProps {
  open: boolean;
  onOpenChange: (open: boolean) => void;
  title: ReactNode;
  description?: ReactNode;
  children: ReactNode;
  footer?: ReactNode;
  size?: keyof typeof SIZES;
  /** While busy the sheet cannot be dismissed (a save is in flight). */
  busy?: boolean;
  className?: string;
}

/**
 * A panel that slides in from the right edge: the console's container for
 * forms and detail views that are too large for a dialog.
 */
export function Sheet({ open, onOpenChange, title, description, children, footer, size = "md", busy = false, className }: SheetProps) {
  const t = useT();
  const guard = (next: boolean) => {
    if (!next && busy) return;
    onOpenChange(next);
  };
  return (
    <DialogPrimitive.Root open={open} onOpenChange={guard}>
      <DialogPortal>
        <DialogOverlay />
        <DialogPrimitive.Content
          aria-describedby={description ? undefined : undefined}
          onEscapeKeyDown={(event) => busy && event.preventDefault()}
          onInteractOutside={(event) => busy && event.preventDefault()}
          className={cn(
            "fixed inset-y-0 right-0 z-50 flex w-full flex-col border-l bg-popover text-popover-foreground shadow-lg outline-none",
            "data-[state=open]:animate-[rio-slide-in-right_220ms_cubic-bezier(0.22,1,0.36,1)] data-[state=closed]:animate-[rio-slide-out-right_160ms_ease-in]",
            SIZES[size],
            className,
          )}
        >
          <header className="flex shrink-0 items-start justify-between gap-3 border-b px-5 py-4">
            <div className="min-w-0 space-y-1">
              <DialogPrimitive.Title className="text-lg font-semibold leading-tight tracking-tight">{title}</DialogPrimitive.Title>
              {description ? (
                <DialogPrimitive.Description className="text-sm text-muted-foreground">{description}</DialogPrimitive.Description>
              ) : null}
            </div>
            <DialogPrimitive.Close
              disabled={busy}
              aria-label={t("common.actions.close")}
              className="inline-flex size-8 shrink-0 items-center justify-center rounded-md text-muted-foreground transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:opacity-50"
            >
              <X className="size-4" />
            </DialogPrimitive.Close>
          </header>
          <div className="min-h-0 flex-1 overflow-y-auto px-5 py-4">{children}</div>
          {footer ? <footer className="flex shrink-0 flex-wrap items-center justify-end gap-2 border-t bg-popover px-5 py-3">{footer}</footer> : null}
        </DialogPrimitive.Content>
      </DialogPortal>
    </DialogPrimitive.Root>
  );
}
