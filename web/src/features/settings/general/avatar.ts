/** Avatar references: external URL, generated variant, or an uploaded file. */

export type AvatarRef =
  | { kind: "empty" }
  | { kind: "url"; url: string }
  | { kind: "generated"; variant: number }
  | { kind: "file"; fileID: string };

const GENERATED_PREFIX = "generated:github:";
const FILE_PREFIX = "file:";

export function parseAvatar(value: string): AvatarRef {
  const v = value.trim();
  if (!v) return { kind: "empty" };
  if (v.startsWith(GENERATED_PREFIX)) {
    const n = Number(v.slice(GENERATED_PREFIX.length));
    if (Number.isInteger(n) && n >= 0) return { kind: "generated", variant: n };
    return { kind: "empty" };
  }
  if (v.startsWith(FILE_PREFIX)) {
    const id = v.slice(FILE_PREFIX.length);
    return id.startsWith("file_") ? { kind: "file", fileID: id } : { kind: "empty" };
  }
  return { kind: "url", url: v };
}

export function generatedAvatar(variant: number): string {
  return `${GENERATED_PREFIX}${Math.max(0, Math.floor(variant))}`;
}

export function fileAvatar(fileID: string): string {
  return `${FILE_PREFIX}${fileID}`;
}

export function randomVariant(): number {
  return Math.floor(Math.random() * 1_000_000);
}

/** First character of the display name, else of the username, else "?". */
export function avatarInitial(displayName: string, username: string): string {
  const first = Array.from(displayName.trim())[0] ?? Array.from(username.trim())[0];
  return first ? first.toUpperCase() : "?";
}

/** Deterministic colours for a generated avatar (hue pair + shape seed). */
export function generatedPalette(variant: number): { from: number; to: number; cells: boolean[] } {
  let x = (variant + 1) * 2654435761 >>> 0;
  const next = () => {
    x ^= x << 13;
    x >>>= 0;
    x ^= x >>> 17;
    x ^= x << 5;
    x >>>= 0;
    return x;
  };
  const from = next() % 360;
  const to = (from + 40 + (next() % 120)) % 360;
  // 5x5 mirrored pattern: 15 independent cells.
  const half: boolean[] = [];
  for (let i = 0; i < 15; i++) half.push(next() % 100 < 55);
  const cells: boolean[] = [];
  for (let row = 0; row < 5; row++) {
    for (let col = 0; col < 5; col++) {
      const c = col < 3 ? col : 4 - col;
      cells.push(half[row * 3 + c] as boolean);
    }
  }
  return { from, to, cells };
}
