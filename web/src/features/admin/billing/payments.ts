/** Payment settings (namespace `billing`). */
import { apiUrl } from "@/lib/api/base-url";
import { problem, type Problem } from "../lib/problem";
import { hasValue, type Draft } from "../lib/settings-form";

export const PROVIDERS = ["stripe", "epay"] as const;
export type PaymentProvider = (typeof PROVIDERS)[number];

/** `disabled` or empty means none; duplicates and unknown names are dropped. */
export function parseProviders(value: string | undefined): PaymentProvider[] {
  const out: PaymentProvider[] = [];
  for (const part of (value ?? "").split(",")) {
    const name = part.trim().toLowerCase();
    if ((PROVIDERS as readonly string[]).includes(name) && !out.includes(name as PaymentProvider)) out.push(name as PaymentProvider);
  }
  return out;
}

export function formatProviders(list: readonly PaymentProvider[]): string {
  const ordered = PROVIDERS.filter((p) => list.includes(p));
  return ordered.length > 0 ? ordered.join(",") : "disabled";
}

export function toggleProvider(value: string | undefined, provider: PaymentProvider, on: boolean): string {
  const current = parseProviders(value).filter((p) => p !== provider);
  return formatProviders(on ? [...current, provider] : current);
}

export const DEFAULT_EPAY_TYPES = '[{"name":"Alipay","type":"alipay"},{"name":"WeChat Pay","type":"wxpay"}]';

const B = (key: string) => `billing.${key}`;
export const P = {
  providers: B("payment_providers"),
  stripeKey: B("stripe_publishable_key"),
  stripeSecret: B("stripe_secret_key"),
  stripeWebhook: B("stripe_webhook_secret"),
  epayUrl: B("epay_gateway_url"),
  epayTypes: B("epay_types"),
  epayPid: B("epay_pid"),
  epayKey: B("epay_key"),
} as const;

/** A non-empty array of objects that each have a non-blank string `name` and `type`. */
export function validEpayTypes(text: string): boolean {
  try {
    const value: unknown = JSON.parse(text);
    return (
      Array.isArray(value) &&
      value.length > 0 &&
      value.every((x) => typeof x === "object" && x !== null && typeof (x as { name?: unknown }).name === "string" && (x as { name: string }).name.trim() !== "" && typeof (x as { type?: unknown }).type === "string" && (x as { type: string }).type.trim() !== "")
    );
  } catch {
    return false;
  }
}

/** Every failure shares one heading; the description says what is missing. */
export function validatePayments(draft: Draft, configured: ReadonlySet<string>): Problem | null {
  const providers = parseProviders(draft[P.providers]);
  if (providers.includes("stripe")) {
    if (!hasValue(draft, configured, P.stripeSecret)) return problem("adminBilling.payments.problems.stripeSecret");
    if (!hasValue(draft, configured, P.stripeWebhook)) return problem("adminBilling.payments.problems.stripeWebhook");
  }
  if (providers.includes("epay")) {
    if (!(draft[P.epayUrl] ?? "").trim()) return problem("adminBilling.payments.problems.epayUrl");
    if (!(draft[P.epayTypes] ?? "").trim() || !validEpayTypes(draft[P.epayTypes] ?? "")) return problem("adminBilling.payments.problems.epayTypes");
    if (!(draft[P.epayPid] ?? "").trim()) return problem("adminBilling.payments.problems.epayPid");
    if (!hasValue(draft, configured, P.epayKey)) return problem("adminBilling.payments.problems.epayKey");
  }
  return null;
}

/** The address Stripe should call; the API may be on the page's own origin. */
export function stripeWebhookUrl(origin: string): string {
  const url = apiUrl("/billing/payments/stripe/webhook");
  return /^https?:\/\//i.test(url) ? url : `${origin.replace(/\/+$/, "")}${url}`;
}
