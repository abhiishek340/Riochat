/** Display helpers shared by the admin screens. All of them are pure. */

export function isValidDate(value: Date): boolean {
  return !Number.isNaN(value.getTime());
}

/** Locale-aware date and time; empty or unparseable input renders "-". */
export function formatDateTime(value: string | number | Date | null | undefined, locale: string): string {
  if (value === null || value === undefined || value === "") return "-";
  const date = value instanceof Date ? value : new Date(value);
  if (!isValidDate(date)) return "-";
  return new Intl.DateTimeFormat(locale, {
    year: "numeric",
    month: "2-digit",
    day: "2-digit",
    hour: "2-digit",
    minute: "2-digit",
  }).format(date);
}

export function formatNumber(value: number | null | undefined, locale: string, maxFractionDigits = 0): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "-";
  return new Intl.NumberFormat(locale, { maximumFractionDigits: maxFractionDigits }).format(value);
}

/** Table money: USD with exactly three decimals and grouping. */
export function formatUsdTable(value: number | null | undefined, locale: string): string {
  if (typeof value !== "number" || !Number.isFinite(value)) return "-";
  return `$${new Intl.NumberFormat(locale, { minimumFractionDigits: 3, maximumFractionDigits: 3 }).format(value)}`;
}

/** Credit amounts: up to two decimals, "$0" when not positive. */
export function formatCredit(value: number | null | undefined, locale: string): string {
  if (typeof value !== "number" || !Number.isFinite(value) || value <= 0) return "$0";
  return `$${new Intl.NumberFormat(locale, { minimumFractionDigits: 0, maximumFractionDigits: 2 }).format(value)}`;
}

/** Amount stored in cents, rendered with the ISO currency code. */
export function formatCents(cents: number | null | undefined, currency: string | null | undefined, locale: string): string {
  if (typeof cents !== "number" || !Number.isFinite(cents)) return "-";
  const amount = cents / 100;
  const code = (currency ?? "").trim().toUpperCase() || "USD";
  try {
    return new Intl.NumberFormat(locale, { style: "currency", currency: code }).format(amount);
  } catch {
    return `${new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: 2 }).format(amount)} ${code}`;
  }
}

export type DisplayCurrency = "USD" | "CNY";

export interface MoneyDisplay {
  currency: DisplayCurrency;
  /** USD to CNY multiplier; ignored unless positive. */
  rate: number;
}

/** CNY display falls back to USD when the rate is missing or not positive. */
export function effectiveCurrency(display: MoneyDisplay): DisplayCurrency {
  return display.currency === "CNY" && Number.isFinite(display.rate) && display.rate > 0 ? "CNY" : "USD";
}

export function formatDisplayMoney(usd: number | null | undefined, display: MoneyDisplay, locale: string, digits = 4): string {
  if (typeof usd !== "number" || !Number.isFinite(usd)) return "-";
  const currency = effectiveCurrency(display);
  const value = currency === "CNY" ? usd * display.rate : usd;
  const symbol = currency === "CNY" ? "¥" : "$";
  return `${symbol}${new Intl.NumberFormat(locale, { minimumFractionDigits: 2, maximumFractionDigits: digits }).format(value)}`;
}

/** Milliseconds below one second, otherwise seconds with up to two decimals. */
export function formatLatency(ms: number | null | undefined, locale: string): string {
  if (typeof ms !== "number" || !Number.isFinite(ms)) return "-";
  if (ms < 1000) return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 0 }).format(ms)} ms`;
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: 2 }).format(ms / 1000)} s`;
}

/** 1536 -> "1.5 KB". Used for file and payload sizes. */
export function formatBytes(bytes: number | null | undefined, locale: string): string {
  if (typeof bytes !== "number" || !Number.isFinite(bytes) || bytes < 0) return "-";
  const units = ["B", "KB", "MB", "GB", "TB"];
  let value = bytes;
  let unit = 0;
  while (value >= 1024 && unit < units.length - 1) {
    value /= 1024;
    unit++;
  }
  return `${new Intl.NumberFormat(locale, { maximumFractionDigits: unit === 0 ? 0 : 1 }).format(value)} ${units[unit]}`;
}

/** Local calendar day as YYYY-MM-DD. */
export function toDateInput(date: Date): string {
  const y = date.getFullYear();
  const m = String(date.getMonth() + 1).padStart(2, "0");
  const d = String(date.getDate()).padStart(2, "0");
  return `${y}-${m}-${d}`;
}

/** Parses YYYY-MM-DD as a real calendar date; anything else is undefined. */
export function parseDateInput(text: string): Date | undefined {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(text.trim());
  if (!match) return undefined;
  const [y, m, d] = [Number(match[1]), Number(match[2]), Number(match[3])];
  const date = new Date(y, m - 1, d);
  if (date.getFullYear() !== y || date.getMonth() !== m - 1 || date.getDate() !== d) return undefined;
  return date;
}

/** Start (00:00:00.000) of a local day as an RFC 3339 instant. */
export function startOfDayIso(text: string): string | undefined {
  const date = parseDateInput(text);
  if (!date) return undefined;
  date.setHours(0, 0, 0, 0);
  return date.toISOString();
}

/** End (23:59:59.999) of a local day as an RFC 3339 instant. */
export function endOfDayIso(text: string): string | undefined {
  const date = parseDateInput(text);
  if (!date) return undefined;
  date.setHours(23, 59, 59, 999);
  return date.toISOString();
}

/** `datetime-local` value (YYYY-MM-DDTHH:mm) for an ISO instant, in local time. */
export function toDateTimeInput(iso: string | null | undefined): string {
  if (!iso) return "";
  const date = new Date(iso);
  if (!isValidDate(date)) return "";
  return `${toDateInput(date)}T${String(date.getHours()).padStart(2, "0")}:${String(date.getMinutes()).padStart(2, "0")}`;
}

/** Converts a `datetime-local` value to an ISO instant; empty or invalid gives undefined. */
export function fromDateTimeInput(text: string): string | undefined {
  if (!text.trim()) return undefined;
  const date = new Date(text);
  return isValidDate(date) ? date.toISOString() : undefined;
}

export function pad2(n: number): string {
  return String(n).padStart(2, "0");
}
