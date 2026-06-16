"use client";

import { tryPretty } from "../lib/json";
import { cn } from "@/lib/utils/cn";
import { CopyButton } from "./copy-button";

/** A read-only code block with a copy button. Pretty-prints JSON text when it parses. */
export function JsonView({ text, className, maxHeight = "max-h-80", pretty = true }: { text: string; className?: string; maxHeight?: string; pretty?: boolean }) {
  const shown = pretty ? tryPretty(text) : text;
  return (
    <div className={cn("relative rounded-lg border bg-muted/40", className)}>
      <div className="absolute right-1.5 top-1.5">
        <CopyButton text={shown} />
      </div>
      <pre className={cn("overflow-auto p-3 pr-10 font-mono text-[12.5px] leading-relaxed", maxHeight)}>
        <code>{shown}</code>
      </pre>
    </div>
  );
}
