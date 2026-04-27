/** Locale-aware formatting helpers shared by the settings pages. */

export function parseDate(value: string | Date | null | undefined): Date | null {
  if (!value) return null;
  const d = value instanceof Date ? value : new Date(value);
  return Number.isNaN(d.getTime()) ? null : d;
}

/** Long date with time, "-" when missing or unparsable. */
export function formatDateTime(value: string | Date | null | undefined, locale: string): string {
  const d = parseDate(value);
  if (!d) return "-";
  return new Intl.DateTimeFormat(locale, {
    year: "numeric",
    month: "long",
    day: "numeric",
    hour: "numeric",
    minute: "2-digit",
  }).format(d);
}

/** Numeric `YYYY/MM/DD HH:mm:ss` in the viewer's time zone (24-hour clock). */
export function formatStamp(value: string | Date | null | undefined): string {
  const d = parseDate(value);
  if (!d) return "-";
  const p = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}/${p(d.getMonth() + 1)}/${p(d.getDate())} ${p(d.getHours())}:${p(d.getMinutes())}:${p(d.getSeconds())}`;
}

export function formatDate(value: string | Date | null | undefined, locale: string): string {
  const d = parseDate(value);
  if (!d) return "-";
  return new Intl.DateTimeFormat(locale, { year: "numeric", month: "short", day: "numeric" }).format(d);
}

export function formatInt(value: number, locale = "en-US"): string {
  return new Intl.NumberFormat(locale).format(Number.isFinite(value) ? value : 0);
}

/** Token counts: "-" for zero or junk (tables), grouped digits otherwise. */
export function formatTokens(value: number | null | undefined): string {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) return "-";
  return new Intl.NumberFormat("en-US").format(value);
}

/** 12_400 -> "12.4K", 3_200_000 -> "3.2M" (chart axes). */
export function abbreviateCount(value: number): string {
  const abs = Math.abs(value);
  if (abs >= 1_000_000_000) return `${trimZero(value / 1_000_000_000)}B`;
  if (abs >= 1_000_000) return `${trimZero(value / 1_000_000)}M`;
  if (abs >= 1_000) return `${trimZero(value / 1_000)}K`;
  return String(Math.round(value));
}
function trimZero(n: number): string {
  return n.toFixed(1).replace(/\.0$/, "");
}

/** "-" when missing, "NNNms" below one second, else seconds with up to two decimals. */
export function formatLatency(ms: number | null | undefined): string {
  if (typeof ms !== "number" || !Number.isFinite(ms) || ms <= 0) return "-";
  if (ms < 1000) return `${Math.round(ms)}ms`;
  return `${new Intl.NumberFormat("en-US", { maximumFractionDigits: 2 }).format(ms / 1000)}s`;
}
