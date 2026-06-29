/**
 * Avatars. `generated:github:<n>` is drawn locally as a symmetric 5x5
 * identicon so it needs no request; `file:<id>` points at the account's avatar
 * route; everything else is used as written.
 */
import { apiUrl } from "@/lib/api/base-url";

const GENERATED = /^generated:github:(\d+)$/;

function fnv1a(text: string): number {
  let h = 0x811c9dc5;
  for (let i = 0; i < text.length; i++) {
    h ^= text.charCodeAt(i);
    h = Math.imul(h, 0x01000193) >>> 0;
  }
  return h >>> 0;
}

function xorshift(seed: number): () => number {
  let s = seed || 1;
  return () => {
    s ^= s << 13;
    s >>>= 0;
    s ^= s >>> 17;
    s ^= s << 5;
    s >>>= 0;
    return s;
  };
}

/** Deterministic 96px identicon as an SVG data address. */
export function identicon(seed: string, variant: number): string {
  const next = xorshift(fnv1a(`${seed}:${variant}`));
  const hue = next() % 360;
  const cells: boolean[][] = Array.from({ length: 5 }, () => Array<boolean>(5).fill(false));
  let filled = 0;
  for (let row = 0; row < 5; row++) {
    for (let col = 0; col < 3; col++) {
      if (next() % 100 < 42) {
        cells[row]![col] = true;
        cells[row]![4 - col] = true;
        filled += col === 2 ? 1 : 2;
      }
    }
  }
  // Keep the picture from being empty or crowded.
  for (let guard = 0; guard < 40 && (filled < 8 || filled > 13); guard++) {
    const row = next() % 5;
    const col = next() % 3;
    const on = filled < 8;
    if (cells[row]![col] === on) continue;
    cells[row]![col] = on;
    cells[row]![4 - col] = on;
    filled += (on ? 1 : -1) * (col === 2 ? 1 : 2);
  }
  const rects: string[] = [];
  for (let row = 0; row < 5; row++) for (let col = 0; col < 5; col++) if (cells[row]![col]) rects.push(`<rect x="${10 + col * 15.2}" y="${10 + row * 15.2}" width="15.2" height="15.2"/>`);
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" width="96" height="96" viewBox="0 0 96 96"><rect width="96" height="96" rx="12" fill="hsl(${(hue + 8) % 360} 20% 90%)"/><g fill="hsl(${hue} 46% 32%)">${rects.join("")}</g></svg>`;
  return `data:image/svg+xml;utf8,${encodeURIComponent(svg)}`;
}

export function randomVariant(): number {
  const buf = new Uint32Array(1);
  if (typeof crypto !== "undefined") crypto.getRandomValues(buf);
  else buf[0] = Math.floor(Math.random() * 2 ** 32);
  return buf[0] as number;
}

export function generatedAvatar(variant: number = randomVariant()): string {
  return `generated:github:${variant}`;
}

export function avatarSrc(ref: string | null | undefined, user: { publicID?: string; username?: string; displayName?: string }): string {
  const value = (ref ?? "").trim();
  const seed = user.publicID || user.username || user.displayName || "riochat-user";
  const generated = GENERATED.exec(value);
  if (generated) return identicon(seed, Number(generated[1]));
  if (!value) return identicon(seed, 0);
  if (value.startsWith("file:")) {
    const id = value.slice(5);
    return id.startsWith("file_") && user.publicID ? apiUrl(`/users/${encodeURIComponent(user.publicID)}/avatar?file=${encodeURIComponent(id)}`) : value;
  }
  return value;
}
