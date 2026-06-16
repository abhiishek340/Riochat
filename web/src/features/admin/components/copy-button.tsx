"use client";

import { Check, Copy } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button, type ButtonProps } from "@/components/ui/button";
import { toast } from "@/components/ui/toast-store";
import { Tooltip } from "@/components/ui/tooltip";
import { useT } from "@/lib/i18n/provider";

/** Writes text to the clipboard; resolves false when the browser refuses. */
export async function copyText(text: string): Promise<boolean> {
  try {
    await navigator.clipboard.writeText(text);
    return true;
  } catch {
    return false;
  }
}

export function CopyButton({ text, label, size = "icon-sm", variant = "ghost", ...props }: { text: string; label?: string } & Omit<ButtonProps, "onClick" | "children">) {
  const t = useT();
  const [done, setDone] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => void (timer.current && clearTimeout(timer.current)), []);
  const name = label ?? t("common.actions.copy");
  return (
    <Tooltip label={done ? t("common.actions.copied") : name}>
      <Button
        size={size}
        variant={variant}
        aria-label={name}
        onClick={async () => {
          if (!(await copyText(text))) {
            toast.error(t("common.errors.copyFailed"));
            return;
          }
          setDone(true);
          if (timer.current) clearTimeout(timer.current);
          timer.current = setTimeout(() => setDone(false), 1500);
        }}
        {...props}
      >
        {done ? <Check /> : <Copy />}
      </Button>
    </Tooltip>
  );
}
