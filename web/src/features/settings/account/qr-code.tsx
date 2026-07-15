"use client";

import { useMemo } from "react";
import { useT } from "@/lib/i18n/provider";
import { encodeQr } from "./qr";

/** Client-side QR image of `value`; a short notice when it cannot be drawn. */
export function QrCode({ value, label }: { value: string; label: string }) {
  const t = useT();
  const matrix = useMemo(() => {
    try {
      return value ? encodeQr(value) : null;
    } catch {
      return null;
    }
  }, [value]);

  if (!matrix) {
    return <p className="rounded-lg border border-dashed p-6 text-center text-sm text-muted-foreground">{t("settings.account.twoFactor.qrUnavailable")}</p>;
  }
  const quiet = 4;
  const size = matrix.length + quiet * 2;
  let path = "";
  matrix.forEach((row, y) =>
    row.forEach((dark, x) => {
      if (dark) path += `M${x + quiet},${y + quiet}h1v1h-1z`;
    }),
  );
  return (
    <svg viewBox={`0 0 ${size} ${size}`} role="img" aria-label={label} shapeRendering="crispEdges" className="mx-auto size-52 rounded-lg bg-white p-0">
      <rect width={size} height={size} fill="#fff" />
      <path d={path} fill="#000" />
    </svg>
  );
}
