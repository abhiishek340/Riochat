"use client";

import { forwardRef, useEffect, useImperativeHandle, useRef, type InputHTMLAttributes } from "react";
import { cn } from "@/lib/utils/cn";

export interface CheckboxProps extends Omit<InputHTMLAttributes<HTMLInputElement>, "type" | "onChange" | "checked"> {
  checked: boolean | "indeterminate";
  onCheckedChange: (checked: boolean) => void;
}

/** Native checkbox with the console's styling and an indeterminate state. */
export const Checkbox = forwardRef<HTMLInputElement, CheckboxProps>(function Checkbox(
  { checked, onCheckedChange, className, ...props },
  ref,
) {
  const inner = useRef<HTMLInputElement>(null);
  useImperativeHandle(ref, () => inner.current as HTMLInputElement);
  useEffect(() => {
    if (inner.current) inner.current.indeterminate = checked === "indeterminate";
  }, [checked]);
  return (
    <input
      ref={inner}
      type="checkbox"
      checked={checked === true}
      onChange={(event) => onCheckedChange(event.target.checked)}
      className={cn("size-4 shrink-0 cursor-pointer rounded border-input accent-[var(--primary)] disabled:cursor-not-allowed disabled:opacity-50", className)}
      {...props}
    />
  );
});
