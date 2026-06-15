import { forwardRef, useEffect, useImperativeHandle, useRef, type InputHTMLAttributes } from "react";
import { cn } from "@/lib/utils/cn";

/** A native checkbox in the app's colours, with a real `indeterminate` state. */
export const Checkbox = forwardRef<HTMLInputElement, Omit<InputHTMLAttributes<HTMLInputElement>, "type"> & { indeterminate?: boolean }>(function Checkbox(
  { className, indeterminate = false, ...props },
  ref,
) {
  const inner = useRef<HTMLInputElement>(null);
  useImperativeHandle(ref, () => inner.current as HTMLInputElement);
  useEffect(() => {
    if (inner.current) inner.current.indeterminate = indeterminate;
  }, [indeterminate]);
  return <input ref={inner} type="checkbox" className={cn("size-4 shrink-0 cursor-pointer rounded border-input accent-primary", className)} {...props} />;
});
