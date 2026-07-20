const UNITS = ["B", "KB", "MB", "GB", "TB"] as const;

/** 1024-based size text: "0 B", "512 B", "1.5 KB", "120 MB". */
export function formatBytes(value: number): string {
  if (!Number.isFinite(value) || value <= 0) return "0 B";
  let size = value;
  let unit = 0;
  while (size >= 1024 && unit < UNITS.length - 1) {
    size /= 1024;
    unit += 1;
  }
  let digits = unit === 0 || size >= 100 ? 0 : 1;
  // 1023.97 KB would print as "1024.0 KB"; carry it over to the next unit instead.
  if (unit > 0 && unit < UNITS.length - 1 && Number(size.toFixed(digits)) >= 1024) {
    size /= 1024;
    unit += 1;
    digits = size >= 100 ? 0 : 1;
  }
  return `${size.toFixed(digits)} ${UNITS[unit]}`;
}

/** Date and time in the app locale; "-" for missing or unparsable input. */
export function formatDateTime(value: string | null | undefined, locale: string): string {
  if (!value) return "-";
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return "-";
  try {
    return new Intl.DateTimeFormat(locale, { year: "numeric", month: "short", day: "numeric", hour: "2-digit", minute: "2-digit" }).format(date);
  } catch {
    return date.toISOString();
  }
}

export interface QuotaView {
  unlimited: boolean;
  used: number;
  total: number;
  percent: number;
  remaining: number;
}

/** Derived numbers for the storage panel. A quota of zero or less means "no limit". */
export function describeQuota(quota: { quotaBytes: number; usedBytes: number }): QuotaView {
  const used = Math.max(0, quota.usedBytes);
  if (quota.quotaBytes <= 0) return { unlimited: true, used, total: 0, percent: 0, remaining: 0 };
  const percent = Math.min(100, Math.max(0, (quota.usedBytes / quota.quotaBytes) * 100));
  return { unlimited: false, used, total: quota.quotaBytes, percent, remaining: Math.max(0, quota.quotaBytes - used) };
}

/** Two uppercase letters standing in for a thumbnail. */
export function nameInitials(name: string): string {
  const base = name.trim().replace(/\.[^.]*$/, "") || name.trim();
  const letters = Array.from(base.replace(/[^\p{L}\p{N}]/gu, "")).slice(0, 2).join("");
  return (letters || "?").toUpperCase();
}
