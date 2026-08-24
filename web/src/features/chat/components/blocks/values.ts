/** Small coercions for props that come from model-written JSON. */

export function str(v: unknown, fallback = ""): string {
  if (typeof v === "string") return v;
  if (typeof v === "number" && Number.isFinite(v)) return String(v);
  if (typeof v === "boolean") return v ? "true" : "false";
  return fallback;
}

export function num(v: unknown, fallback: number): number {
  if (typeof v === "number" && Number.isFinite(v)) return v;
  if (typeof v === "string" && v.trim() !== "" && Number.isFinite(Number(v))) return Number(v);
  return fallback;
}

export function bool(v: unknown, fallback = false): boolean {
  return typeof v === "boolean" ? v : fallback;
}

export function list(v: unknown): unknown[] {
  return Array.isArray(v) ? v : [];
}

export function rec(v: unknown): Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v) ? (v as Record<string, unknown>) : {};
}

export function display(v: unknown): string {
  if (v === null || v === undefined) return "";
  if (typeof v === "number") return Number.isInteger(v) ? String(v) : String(Math.round(v * 1e6) / 1e6);
  if (typeof v === "object") return JSON.stringify(v);
  return String(v);
}

/** Only http(s) links, site-relative paths and image data URLs are shown in galleries. */
export function safeImageSrc(v: unknown): string {
  const s = str(v).trim();
  if (/^https?:\/\//i.test(s) || /^\/(?!\/)/.test(s) || /^data:image\/(png|jpe?g|gif|webp|avif);base64,/i.test(s)) return s;
  return "";
}

export const TONES = ["neutral", "info", "success", "warning", "danger"] as const;
export type Tone = (typeof TONES)[number];

export function tone(v: unknown): Tone {
  return (TONES as readonly string[]).includes(v as string) ? (v as Tone) : "neutral";
}

export const TONE_CLASS: Record<Tone, string> = {
  neutral: "border-border bg-muted/50 text-foreground",
  info: "border-info/30 bg-info/10 text-foreground",
  success: "border-success/30 bg-success/10 text-foreground",
  warning: "border-warning/40 bg-warning/10 text-foreground",
  danger: "border-destructive/30 bg-destructive/10 text-foreground",
};
