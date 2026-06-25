/** Small display formatters for the shared view. */

export function formatShareDate(iso: string, locale: string): string {
  const time = new Date(iso);
  if (Number.isNaN(time.getTime())) return "";
  return new Intl.DateTimeFormat(locale, { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(time);
}

export function formatLatency(ms: number): string {
  if (!(ms > 0)) return "";
  return ms < 1000 ? `${Math.round(ms)} ms` : `${(ms / 1000).toFixed(ms < 10_000 ? 1 : 0)} s`;
}

export function formatCount(n: number, locale: string): string {
  return new Intl.NumberFormat(locale, { notation: n >= 10_000 ? "compact" : "standard" }).format(n);
}

export function formatBytes(bytes: number): string {
  if (!(bytes > 0)) return "";
  if (bytes < 1024) return `${bytes} B`;
  if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
  return `${(bytes / 1024 / 1024).toFixed(1)} MB`;
}
