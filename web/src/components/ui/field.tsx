"use client";

import { createContext, useContext, useId, type HTMLAttributes, type LabelHTMLAttributes, type ReactNode } from "react";
import { cn } from "@/lib/utils/cn";

/**
 * Form helpers. `<Field>` generates ids and wires them to the label, hint and
 * error, so any `Input`, `Textarea` or `NativeSelect` inside picks up `id`,
 * `aria-describedby` and `aria-invalid` without manual plumbing.
 *
 *   <Field invalid={!!error}>
 *     <FieldLabel>Name</FieldLabel>
 *     <Input value={name} onChange={...} />
 *     <FieldHint>Shown to teammates.</FieldHint>
 *     <FieldError>{error}</FieldError>
 *   </Field>
 */

interface FieldContextValue {
  controlId: string;
  hintId: string;
  errorId: string;
  invalid: boolean;
  required: boolean;
}

const FieldContext = createContext<FieldContextValue | null>(null);

interface ControlProps {
  id?: string;
  required?: boolean;
  "aria-invalid"?: boolean | "true" | "false" | "grammar" | "spelling";
  "aria-describedby"?: string;
}

/** Merges field wiring into a control's own props (explicit props win). */
export function useFieldControl<P extends ControlProps>(props: P): P {
  const field = useContext(FieldContext);
  if (!field) return props;
  const described = [props["aria-describedby"], field.invalid ? field.errorId : undefined, field.hintId].filter(Boolean).join(" ");
  return {
    ...props,
    id: props.id ?? field.controlId,
    required: props.required ?? (field.required || undefined),
    "aria-invalid": props["aria-invalid"] ?? (field.invalid || undefined),
    "aria-describedby": described || undefined,
  };
}

export function Field({
  invalid = false,
  required = false,
  className,
  children,
  ...props
}: HTMLAttributes<HTMLDivElement> & { invalid?: boolean; required?: boolean; children: ReactNode }) {
  const base = useId();
  const value: FieldContextValue = {
    controlId: `${base}-control`,
    hintId: `${base}-hint`,
    errorId: `${base}-error`,
    invalid,
    required,
  };
  return (
    <FieldContext.Provider value={value}>
      <div className={cn("grid gap-1.5", className)} {...props}>
        {children}
      </div>
    </FieldContext.Provider>
  );
}

export function FieldLabel({ className, children, ...props }: LabelHTMLAttributes<HTMLLabelElement>) {
  const field = useContext(FieldContext);
  return (
    <label htmlFor={field?.controlId} className={cn("text-sm font-medium leading-none", className)} {...props}>
      {children}
      {field?.required ? (
        <span aria-hidden className="ml-0.5 text-destructive">
          *
        </span>
      ) : null}
    </label>
  );
}

export function FieldHint({ className, ...props }: HTMLAttributes<HTMLParagraphElement>) {
  const field = useContext(FieldContext);
  return <p id={field?.hintId} className={cn("text-[13px] text-muted-foreground", className)} {...props} />;
}

/** Renders nothing when it has no children, so it can always be present. */
export function FieldError({ className, children, ...props }: HTMLAttributes<HTMLParagraphElement>) {
  const field = useContext(FieldContext);
  if (!children) return null;
  return (
    <p id={field?.errorId} role="alert" className={cn("text-[13px] font-medium text-destructive", className)} {...props}>
      {children}
    </p>
  );
}

export function FieldGroup({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("grid gap-4", className)} {...props} />;
}

export function FormActions({ className, ...props }: HTMLAttributes<HTMLDivElement>) {
  return <div className={cn("flex flex-wrap items-center justify-end gap-2 pt-2", className)} {...props} />;
}
