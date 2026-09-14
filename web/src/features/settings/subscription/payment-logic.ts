/** Payment methods on offer and how a selection survives config changes. */
import type { BillingConfig, EpayType, PaymentProvider } from "./types";

export interface MethodOption {
  provider: PaymentProvider;
  /** Only for EPay. */
  epayType?: string;
  /** Configured name, else a known label key, else the raw type. */
  label: { name: string } | { known: "alipay" | "wxpay" | "qqpay" } | { custom: string } | { stripe: true };
}

export const DEFAULT_EPAY_TYPES: readonly EpayType[] = [
  { type: "alipay", name: "" },
  { type: "wxpay", name: "" },
];

const KNOWN = new Set(["alipay", "wxpay", "qqpay"]);

/** Top-up is offered once the config is in and at least one valid provider exists. */
export function paymentEnabled(config: BillingConfig | null): boolean {
  return config !== null && config.paymentProviders.length > 0;
}

export function methodOptions(config: BillingConfig | null): MethodOption[] {
  if (!config) return [];
  const out: MethodOption[] = [];
  if (config.paymentProviders.includes("stripe")) out.push({ provider: "stripe", label: { stripe: true } });
  if (config.paymentProviders.includes("epay")) {
    const types = config.epayTypes.filter((e) => e.type.trim() !== "");
    for (const e of types.length ? types : DEFAULT_EPAY_TYPES) {
      const label: MethodOption["label"] = e.name ? { name: e.name } : KNOWN.has(e.type) ? { known: e.type as "alipay" | "wxpay" | "qqpay" } : { custom: e.type };
      out.push({ provider: "epay", epayType: e.type, label });
    }
  }
  return out;
}

export interface MethodSelection {
  provider: PaymentProvider;
  epayType: string;
}

export const DEFAULT_SELECTION: MethodSelection = { provider: "stripe", epayType: "alipay" };

export function sameMethod(a: MethodSelection, o: MethodOption): boolean {
  return a.provider === o.provider && (o.provider === "stripe" || a.epayType === o.epayType);
}

/** Keeps the selection when still offered; otherwise falls back to the first configured method. */
export function reconcileSelection(selection: MethodSelection, options: readonly MethodOption[]): MethodSelection {
  if (options.length === 0) return selection;
  if (options.some((o) => sameMethod(selection, o))) return selection;
  const sameProvider = options.find((o) => o.provider === selection.provider);
  const pick = sameProvider ?? (options[0] as MethodOption);
  return { provider: pick.provider, epayType: pick.epayType ?? options.find((o) => o.provider === "epay")?.epayType ?? "alipay" };
}

export function checkoutUrls(origin: string): { successURL: string; cancelURL: string } {
  const base = origin.replace(/\/+$/, "");
  return { successURL: `${base}/settings/subscription?payment=success`, cancelURL: `${base}/settings/subscription?payment=cancel` };
}
