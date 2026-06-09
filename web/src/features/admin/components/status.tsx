"use client";

import { Badge } from "@/components/ui/badge";
import { useLabel } from "../lib/use-label";

type Tone = "neutral" | "primary" | "success" | "warning" | "danger" | "outline";

const TONES: Record<string, Tone> = {
  active: "success",
  enabled: "success",
  success: "success",
  available: "success",
  ready: "success",
  running: "success",
  paid: "success",
  allowed: "success",
  passed: "success",
  inactive: "neutral",
  disabled: "neutral",
  unconfigured: "neutral",
  pending: "warning",
  trialing: "primary",
  past_due: "warning",
  pending_activation: "warning",
  locked: "warning",
  frozen: "warning",
  unhealthy: "warning",
  failed_open: "warning",
  suspended: "danger",
  failed: "danger",
  failure: "danger",
  blocked: "danger",
  error: "danger",
  hit: "danger",
  unpaid: "danger",
  canceled: "neutral",
  closed: "neutral",
  deactivated: "neutral",
  unsupported: "outline",
};

export function toneFor(value: string | null | undefined): Tone {
  return TONES[(value ?? "").toLowerCase()] ?? "neutral";
}

/** A coloured pill for an enum value; text comes from `<prefix>.<value>` or the raw value. */
export function StatusBadge({ value, prefix = "adminCommon.status", tone }: { value: string | null | undefined; prefix?: string; tone?: Tone }) {
  const label = useLabel();
  return <Badge tone={tone ?? toneFor(value)}>{label(prefix, value)}</Badge>;
}
