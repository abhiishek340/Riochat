/** Number, size, time and latency formatting. */

/** Below 10 s: one decimal with a floor of 0.1; otherwise whole seconds. */
export function formatLatency(ms: number): string {
  if (!Number.isFinite(ms) || ms <= 0) return "";
  const seconds = ms / 1000;
  if (seconds < 10) return `${Math.max(0.1, Math.floor(seconds * 10) / 10).toFixed(1)}s`;
  return `${Math.round(seconds)}s`;
}

export function formatBytes(bytes: number): string {
  if (!Number.isFinite(bytes) || bytes < 0) return "";
  if (bytes < 1024) return `${bytes} B`;
  const units = ["KB", "MB", "GB", "TB"];
  let value = bytes / 1024;
  let i = 0;
  while (value >= 1024 && i < units.length - 1) {
    value /= 1024;
    i++;
  }
  return `${value >= 100 ? value.toFixed(0) : value.toFixed(1).replace(/\.0$/, "")} ${units[i]}`;
}

export function formatCount(n: number, locale?: string): string {
  return new Intl.NumberFormat(locale).format(n);
}

export function formatDuration(ms: number): string {
  if (!Number.isFinite(ms) || ms < 0) return "";
  const s = Math.round(ms / 1000);
  if (s < 60) return `${s}s`;
  const m = Math.floor(s / 60);
  const rest = s % 60;
  return rest ? `${m}m ${rest}s` : `${m}m`;
}

/**
 * Message timestamp: today shows the time, this year month/day plus time,
 * anything older the full date and time.
 */
export function formatMessageTime(iso: string, now: Date, locale: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  const sameDay = date.toDateString() === now.toDateString();
  if (sameDay) return new Intl.DateTimeFormat(locale, { hour: "2-digit", minute: "2-digit" }).format(date);
  if (date.getFullYear() === now.getFullYear()) {
    return new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(date);
  }
  return formatFullTime(iso, locale);
}

export function formatFullTime(iso: string, locale: string): string {
  const date = new Date(iso);
  if (Number.isNaN(date.getTime())) return "";
  return new Intl.DateTimeFormat(locale, { dateStyle: "medium", timeStyle: "short" }).format(date);
}

/** Elapsed time shown while a reply is live, else recorded latency, else updated-created. */
export function replyLatencyMs(args: { live: boolean; startedAt: number; now: number; latencyMs: number; createdAt: string; updatedAt: string }): number {
  if (args.live) return Math.max(0, args.now - args.startedAt);
  if (args.latencyMs > 0) return args.latencyMs;
  const created = Date.parse(args.createdAt);
  const updated = Date.parse(args.updatedAt);
  if (Number.isFinite(created) && Number.isFinite(updated) && updated > created) return updated - created;
  return 0;
}

export type DayPeriod = "morning" | "afternoon" | "evening" | "night";

/** Hour (0-23) of `now` in an IANA zone; invalid zone falls back to UTC. */
export function hourInZone(now: Date, timeZone: string): number {
  const read = (zone: string): number | null => {
    try {
      const parts = new Intl.DateTimeFormat("en-US", { hour: "numeric", hourCycle: "h23", timeZone: zone }).formatToParts(now);
      const hour = parts.find((p) => p.type === "hour")?.value;
      const n = hour === undefined ? NaN : Number(hour);
      return Number.isFinite(n) ? n % 24 : null;
    } catch {
      return null;
    }
  };
  return read(timeZone || "Etc/UTC") ?? read("Etc/UTC") ?? now.getUTCHours();
}

export function dayPeriod(now: Date, timeZone: string): DayPeriod {
  const h = hourInZone(now, timeZone);
  if (h >= 6 && h < 12) return "morning";
  if (h >= 12 && h < 18) return "afternoon";
  if (h >= 18) return "evening";
  return "night";
}

/** `CSV`-free file name for downloads: unsafe characters replaced, runs collapsed. */
export function safeFileName(name: string, fallback: string): string {
  const cleaned = name
    .replace(/[\\/:*?"<>|]/g, "-")
    .replace(/\s+/g, "-")
    .replace(/-{2,}/g, "-")
    .replace(/^-+|-+$/g, "");
  return cleaned || fallback;
}
