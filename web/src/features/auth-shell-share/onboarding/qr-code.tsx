"use client";

import { useMemo } from "react";
import { useT } from "@/lib/i18n/provider";
import { encodeQr, qrToSvg } from "../lib/qr";

/** Local QR rendering: the secret never leaves the page. Falls back to a note when the text does not fit. */
export function QrCode({ text, className }: { text: string; className?: string }) {
  const t = useT();
  const svg = useMemo(() => {
    const matrix = text ? encodeQr(text) : null;
    return matrix ? qrToSvg(matrix) : null;
  }, [text]);
  if (!svg) {
    return (
      <p className="rounded-lg border border-dashed p-3 text-center text-[13px] text-muted-foreground">{t("onboarding.twoFactor.qrUnavailable")}</p>
    );
  }
  return (
    <svg
      role="img"
      aria-label={t("onboarding.twoFactor.qrLabel")}
      viewBox={`0 0 ${svg.size} ${svg.size}`}
      shapeRendering="crispEdges"
      className={className}
    >
      <rect width={svg.size} height={svg.size} fill="#fff" />
      <path d={svg.path} fill="#111" />
    </svg>
  );
}
