import { forwardRef, type InputHTMLAttributes, type TextareaHTMLAttributes } from "react";
import { cn } from "@/lib/utils/cn";
import { useFieldControl } from "./field";

const control =
  "w-full rounded-md border border-input bg-background px-3 text-sm text-foreground shadow-sm transition-[border-color,box-shadow] duration-150 placeholder:text-muted-foreground/70 focus-visible:border-ring focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring/40 disabled:cursor-not-allowed disabled:opacity-50 aria-invalid:border-destructive aria-invalid:ring-destructive/25 read-only:bg-muted/50";

export const Input = forwardRef<HTMLInputElement, InputHTMLAttributes<HTMLInputElement>>(function Input(
  { className, ...props },
  ref,
) {
  const field = useFieldControl(props);
  return <input ref={ref} className={cn(control, "h-9", className)} {...field} />;
});

export const Textarea = forwardRef<HTMLTextAreaElement, TextareaHTMLAttributes<HTMLTextAreaElement>>(function Textarea(
  { className, ...props },
  ref,
) {
  const field = useFieldControl(props);
  return <textarea ref={ref} className={cn(control, "min-h-20 py-2 leading-relaxed", className)} {...field} />;
});

export const NativeSelect = forwardRef<HTMLSelectElement, React.SelectHTMLAttributes<HTMLSelectElement>>(function NativeSelect(
  { className, children, ...props },
  ref,
) {
  const field = useFieldControl(props);
  return (
    <select ref={ref} className={cn(control, "h-9 appearance-none bg-no-repeat pr-8", className)} {...field}>
      {children}
    </select>
  );
});
