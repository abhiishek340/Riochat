/**
 * Money rules. Amounts are stored in USD; the display currency (USD or CNY)
 * only converts when a usable rate is configured.
 */
import type { BillingConfig, PaymentProvider } from "./types";

export const FALLBACK_EPAY_RATE = 7.2;

export interface Display {
  currency: "USD" | "CNY";
  rate: number;
}

export function isUsableRate(rate: number): boolean {
  return Number.isFinite(rate) && rate > 0;
}

/** CNY only when requested and the rate is a positive finite number. */
export function displayOf(config: Pick<BillingConfig, "displayCurrency" | "usdToCNYRate"> | null): Display {
  if (config && config.displayCurrency === "CNY" && isUsableRate(config.usdToCNYRate)) return { currency: "CNY", rate: config.usdToCNYRate };
  return { currency: "USD", rate: 1 };
}

export function toDisplay(usd: number, display: Display): number {
  return display.currency === "CNY" ? usd * display.rate : usd;
}

export function fromDisplay(amount: number, display: Display): number {
  return display.currency === "CNY" ? amount / display.rate : amount;
}

export function currencySymbol(currency: string): string {
  return currency === "CNY" ? "¥" : currency === "USD" ? "$" : `${currency} `;
}

/** Amount with up to four fraction digits, at least two. */
export function formatMoney(usd: number, display: Display, locale = "en-US"): string {
  const value = toDisplay(Number.isFinite(usd) ? usd : 0, display);
  return new Intl.NumberFormat(locale, {
    style: "currency",
    currency: display.currency,
    minimumFractionDigits: 2,
    maximumFractionDigits: 4,
  }).format(value);
}

/** Cost for summaries: zero shows as zero, tiny values as "< 0.0001". */
export function formatCost(usd: number, display: Display, locale = "en-US"): string {
  const value = toDisplay(Number.isFinite(usd) ? usd : 0, display);
  const fmt = (n: number, min: number) =>
    new Intl.NumberFormat(locale, {
      style: "currency",
      currency: display.currency,
      minimumFractionDigits: min,
      maximumFractionDigits: 4,
    }).format(n);
  if (value === 0) return fmt(0, 2);
  if (Math.abs(value) < 0.0001) return `< ${fmt(0.0001, 4)}`;
  return fmt(value, 2);
}

/** `<amount> / <interval>` for a plan price; "-" without a price. */
export function priceAmountText(
  price: { amountCents: number; currency: string } | undefined,
  display: Display,
  locale = "en-US",
): string {
  if (!price) return "-";
  const major = price.amountCents / 100;
  if (price.currency === "USD") return formatMoney(major, display, locale);
  try {
    return new Intl.NumberFormat("en-US", { style: "currency", currency: price.currency }).format(major);
  } catch {
    return `${major.toFixed(2)} ${price.currency}`;
  }
}

export type Interval = "lifetime" | "year" | "month";
export function normalizeInterval(raw: string): Interval {
  const v = raw.trim().toLowerCase();
  if (v === "lifetime") return "lifetime";
  if (v === "year" || v === "yearly" || v === "annual") return "year";
  return "month";
}

export interface Payable {
  currency: "USD" | "CNY";
  amount: number;
}

/** What a provider charges for `usd`. Stripe: USD unless the display is CNY; EPay: always CNY. */
export function payableFor(provider: PaymentProvider, usd: number, display: Display): Payable {
  if (provider === "stripe") {
    return display.currency === "CNY" ? { currency: "CNY", amount: usd * display.rate } : { currency: "USD", amount: usd };
  }
  const rate = display.currency === "CNY" ? display.rate : FALLBACK_EPAY_RATE;
  return { currency: "CNY", amount: usd * rate };
}

export function formatPayable(p: Payable, locale = "en-US"): string {
  return new Intl.NumberFormat(locale, { style: "currency", currency: p.currency }).format(p.amount);
}

/** Minor units (cents) of an amount typed in the display currency. */
export function minorUnits(amount: number): number {
  return Math.round(amount * 100);
}

export function parseAmount(text: string): number | null {
  const trimmed = text.trim();
  if (!trimmed) return null;
  const n = Number(trimmed);
  return Number.isFinite(n) ? n : null;
}

/** True when a top-up of `amount` (display currency) converts to at least one minor unit. */
export function isValidTopup(amount: number | null): amount is number {
  return amount !== null && amount > 0 && minorUnits(amount) > 0;
}
