"use client";

import { useState } from "react";
import { apiUrl } from "@/lib/api/base-url";
import { cn } from "@/lib/utils/cn";

const ASSET = /^asset:(ico_[0-9a-f]{32})$/i;

/**
 * Resolves an icon reference (managed asset, absolute URL or root-relative
 * path) into an image address. Slugs and empty values have no image and fall
 * back to the initial letter.
 */
export function iconSrc(ref: string | null | undefined): string | null {
  const value = (ref ?? "").trim();
  if (!value) return null;
  const asset = ASSET.exec(value);
  if (asset?.[1]) return apiUrl(`/llm/icon-assets/${asset[1].toLowerCase()}`);
  if (/^https?:\/\//i.test(value)) return value;
  if (value.startsWith("/") && !value.startsWith("//")) return value;
  return null;
}

export function ModelIcon({ icon, name, size = 20, className }: { icon?: string | null; name?: string; size?: number; className?: string }) {
  const src = iconSrc(icon);
  const [failed, setFailed] = useState<string | null>(null);
  const initial = (name || icon || "?").trim().charAt(0).toUpperCase() || "?";
  const style = { width: size, height: size, fontSize: Math.max(10, Math.round(size * 0.5)) };
  if (src && failed !== src) {
    // eslint-disable-next-line @next/next/no-img-element
    return <img src={src} alt="" width={size} height={size} loading="lazy" referrerPolicy="no-referrer" onError={() => setFailed(src)} style={{ width: size, height: size }} className={cn("shrink-0 rounded object-contain", className)} />;
  }
  return (
    <span aria-hidden style={style} className={cn("inline-flex shrink-0 items-center justify-center rounded bg-secondary font-semibold text-secondary-foreground", className)}>
      {initial}
    </span>
  );
}
