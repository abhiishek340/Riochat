"use client";

import { absolutizeApiPath } from "@/lib/api/base-url";
import { cn } from "@/lib/utils/cn";
import { avatarInitial, generatedPalette, parseAvatar } from "./avatar";

/** Renders an avatar reference; `previews` maps uploaded file ids to local object URLs. */
export function AvatarView({
  value,
  displayName,
  username,
  publicID,
  version,
  previews,
  className,
}: {
  value: string;
  displayName: string;
  username: string;
  publicID?: string;
  /** Cache-buster for the server-hosted image. */
  version?: string;
  previews?: Record<string, string>;
  className?: string;
}) {
  const ref = parseAvatar(value);
  const frame = cn("size-full rounded-full object-cover", className);

  if (ref.kind === "url") {
    const src = absolutizeApiPath(ref.url);
    // eslint-disable-next-line @next/next/no-img-element -- remote avatar of unknown origin
    return <img src={src} alt="" className={frame} referrerPolicy="no-referrer" />;
  }
  if (ref.kind === "file") {
    const local = previews?.[ref.fileID];
    const src = local ?? (publicID ? absolutizeApiPath(`/api/v1/users/${encodeURIComponent(publicID)}/avatar${version ? `?v=${encodeURIComponent(version)}` : ""}`) : "");
    if (src) {
      // eslint-disable-next-line @next/next/no-img-element -- uploaded avatar, possibly a blob: URL
      return <img src={src} alt="" className={frame} />;
    }
  }
  if (ref.kind === "generated") {
    const { from, to, cells } = generatedPalette(ref.variant);
    return (
      <svg viewBox="0 0 50 50" role="img" aria-hidden className={frame}>
        <defs>
          <linearGradient id={`avatar-grad-${ref.variant}`} x1="0" y1="0" x2="1" y2="1">
            <stop offset="0" stopColor={`oklch(0.72 0.12 ${from})`} />
            <stop offset="1" stopColor={`oklch(0.6 0.14 ${to})`} />
          </linearGradient>
        </defs>
        <rect width="50" height="50" fill={`url(#avatar-grad-${ref.variant})`} />
        {cells.map((on, i) =>
          on ? <rect key={i} x={5 + (i % 5) * 8} y={5 + Math.floor(i / 5) * 8} width="8" height="8" fill="oklch(1 0 0 / 0.78)" /> : null,
        )}
      </svg>
    );
  }
  const hue = Array.from(publicID || username || displayName).reduce((h, c) => (h * 31 + c.charCodeAt(0)) % 360, 7);
  return (
    <span
      aria-hidden
      className={cn("flex size-full items-center justify-center rounded-full text-[1.15em] font-semibold text-white", className)}
      style={{ background: `oklch(0.55 0.1 ${hue})` }}
    >
      {avatarInitial(displayName, username)}
    </span>
  );
}
