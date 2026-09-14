/** Pure helpers of the conversation screenshot. */

export const MAX_ROWS = 100;
export const MAX_SIDE = 16384;
export const MAX_AREA = 24 * 1024 * 1024;
export const DEFAULT_SCALE = 2;
export const PADDING = 24;

export class TooLargeError extends Error {
  constructor() {
    super("too large");
    this.name = "TooLargeError";
  }
}

/** The largest scale not above `want` that keeps both the side and area limits; below 1 the capture is refused. */
export function fitScale(width: number, height: number, want = DEFAULT_SCALE): number {
  if (!(width > 0) || !(height > 0)) throw new TooLargeError();
  let scale = want;
  scale = Math.min(scale, MAX_SIDE / width, MAX_SIDE / height, Math.sqrt(MAX_AREA / (width * height)));
  if (scale < 1) throw new TooLargeError();
  return scale;
}

const pad2 = (n: number) => String(n).padStart(2, "0");

export function screenshotFileName(title: string, date = new Date()): string {
  const cleaned = title
    .replace(/[\\/:*?"<>|]/g, "-")
    .replace(/\s+/g, "-")
    .replace(/-{2,}/g, "-")
    .replace(/^-+|-+$/g, "");
  const stamp = `${date.getFullYear()}${pad2(date.getMonth() + 1)}${pad2(date.getDate())}-${pad2(date.getHours())}${pad2(date.getMinutes())}${pad2(date.getSeconds())}`;
  return `conversation-${cleaned || "conversation"}-${stamp}.png`;
}

/** Ids a capture may use: persisted, settled messages, newest `MAX_ROWS`. */
export function capturable<T extends { id: string; persisted: boolean; status: string }>(path: readonly T[]): string[] {
  return path.filter((m) => m.persisted && m.status !== "pending").slice(-MAX_ROWS).map((m) => m.id);
}
