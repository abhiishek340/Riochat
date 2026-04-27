"use client";

import { Check, Copy } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { Button, type ButtonProps } from "@/components/ui/button";
import { toast } from "@/components/ui/toast-store";
import { Tooltip } from "@/components/ui/tooltip";
import { useT } from "@/lib/i18n/provider";
import { copyText } from "../lib/clipboard";

/** Icon button that copies `value`; reports success or failure with a toast. */
export function CopyButton({
  value,
  label,
  successMessage,
  disabled,
  size = "icon-sm",
  variant = "ghost",
}: {
  value: string;
  label?: string;
  successMessage?: string;
  disabled?: boolean;
  size?: ButtonProps["size"];
  variant?: ButtonProps["variant"];
}) {
  const t = useT();
  const [done, setDone] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(() => () => {
    if (timer.current) clearTimeout(timer.current);
  }, []);
  const text = label ?? t("common.actions.copy");
  return (
    <Tooltip label={text}>
      <Button
        variant={variant}
        size={size}
        aria-label={text}
        disabled={disabled || !value}
        onClick={async () => {
          const ok = await copyText(value);
          if (!ok) {
            toast.error(t("common.errors.copyFailed"));
            return;
          }
          toast.success(successMessage ?? t("common.actions.copied"));
          setDone(true);
          if (timer.current) clearTimeout(timer.current);
          timer.current = setTimeout(() => setDone(false), 1500);
        }}
      >
        {done ? <Check /> : <Copy />}
      </Button>
    </Tooltip>
  );
}
