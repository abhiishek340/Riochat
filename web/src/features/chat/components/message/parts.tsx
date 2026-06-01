"use client";

import { forwardRef, type ReactNode } from "react";
import { ChevronLeft, ChevronRight } from "lucide-react";
import { Tooltip } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { formatFullTime, formatMessageTime } from "../../lib/format";
import type { Navigator } from "../../lib/tree";
import { useMessageEnv } from "./env";

/** A small icon button with a tooltip label. */
export const IconAction = forwardRef<
  HTMLButtonElement,
  { label: string; onClick?: () => void; disabled?: boolean; active?: boolean; children: ReactNode; className?: string }
>(function IconAction({ label, onClick, disabled, active, children, className }, ref) {
  return (
    <Tooltip label={label}>
      <button
        ref={ref}
        type="button"
        aria-label={label}
        aria-pressed={active === undefined ? undefined : active}
        disabled={disabled}
        onClick={onClick}
        className={cn(
          "inline-flex size-7 items-center justify-center rounded-md text-muted-foreground outline-none transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring disabled:pointer-events-none disabled:opacity-40 [&_svg]:size-3.5",
          active && "bg-accent text-accent-foreground",
          className,
        )}
      >
        {children}
      </button>
    </Tooltip>
  );
});

export function BranchNav({ nav, onCycle, disabled }: { nav: Navigator; onCycle: (delta: -1 | 1) => void; disabled?: boolean }) {
  const t = useT();
  return (
    <span className="inline-flex items-center text-xs tabular-nums text-muted-foreground" role="group" aria-label={`${nav.index}/${nav.total}`}>
      <IconAction label={t("chatMsg.previousBranch")} onClick={() => onCycle(-1)} disabled={disabled || !nav.canPrevious} className="size-6">
        <ChevronLeft />
      </IconAction>
      <span aria-live="polite">
        {nav.index}/{nav.total}
      </span>
      <IconAction label={t("chatMsg.nextBranch")} onClick={() => onCycle(1)} disabled={disabled || !nav.canNext} className="size-6">
        <ChevronRight />
      </IconAction>
    </span>
  );
}

export function MessageTime({ iso }: { iso: string }) {
  const env = useMessageEnv();
  const text = formatMessageTime(iso, new Date(), env.locale);
  if (!text) return null;
  return (
    <Tooltip label={formatFullTime(iso, env.locale)}>
      <time dateTime={iso} className="text-xs text-muted-foreground" data-capture-time>
        {text}
      </time>
    </Tooltip>
  );
}
