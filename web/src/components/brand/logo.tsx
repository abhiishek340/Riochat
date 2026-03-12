"use client";

import { useBranding } from "@/lib/branding/provider";
import { absolutizeApiPath } from "@/lib/api/base-url";
import { cn } from "@/lib/utils/cn";

/** The built-in mark: a chat bubble carrying two river ripples. */
export function BrandMark({ className }: { className?: string }) {
  return (
    <svg viewBox="0 0 64 64" role="img" aria-label="Riochat" className={cn("size-8 shrink-0", className)}>
      <defs>
        <linearGradient id="rio-mark-g" x1="0" y1="0" x2="1" y2="1">
          <stop offset="0" stopColor="#1aa6b7" />
          <stop offset="1" stopColor="#1c4586" />
        </linearGradient>
      </defs>
      <rect width="64" height="64" rx="15" fill="url(#rio-mark-g)" />
      <rect x="13" y="14" width="38" height="28" rx="10" fill="#fafcfc" />
      <path d="M21 41L19 52L32 41Z" fill="#fafcfc" />
      <path
        d="M20 25.5c3-3.2 6-3.2 9 0s6 3.2 9 0 4.5-2.4 6 0M20 34.5c3-3.2 6-3.2 9 0s5 2.6 9 0"
        fill="none"
        stroke="#1d6f9b"
        strokeWidth="3.4"
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}

/** Operator-supplied logo when branding has one, else the built-in mark. */
export function AppLogo({ className, alt }: { className?: string; alt?: string }) {
  const { branding } = useBranding();
  if (branding.logoURL) {
    return (
      // eslint-disable-next-line @next/next/no-img-element -- operator-supplied URL of unknown origin
      <img src={absolutizeApiPath(branding.logoURL)} alt={alt ?? branding.title} className={cn("size-8 shrink-0 object-contain", className)} />
    );
  }
  return <BrandMark className={className} />;
}
