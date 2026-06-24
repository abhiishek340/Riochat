/** Wire shapes shared by several admin screens. */

export type BillingMode = "self" | "period" | "usage";

export function billingModeOf(value: unknown): BillingMode {
  return value === "period" || value === "usage" ? value : "self";
}

export interface NativeToolPrice {
  provider: string;
  toolKey: string;
  label: string;
  description: string;
  type: string;
  priceNanousd: number;
  unit: string;
  priceLabel: string;
  billable: boolean;
}

export interface BillingConfig {
  mode: BillingMode;
  prepaidAmountUSD: number;
  prepaidAmountNanousd: number;
  usdToCNYRate: number;
  displayCurrency: "USD" | "CNY";
  nativeToolBillingEnabled: boolean;
  nativeToolPricing: NativeToolPrice[];
  paymentProviders: string[];
  epayTypes: Array<{ name: string; type: string }>;
}

/** What the screens assume before the configuration has loaded. */
export const DEFAULT_BILLING_CONFIG: BillingConfig = {
  mode: "self",
  prepaidAmountUSD: 0,
  prepaidAmountNanousd: 0,
  usdToCNYRate: 7.2,
  displayCurrency: "USD",
  nativeToolBillingEnabled: true,
  nativeToolPricing: [],
  paymentProviders: [],
  epayTypes: [],
};

export function readBillingConfig(raw: unknown): BillingConfig {
  const c = (typeof raw === "object" && raw !== null ? raw : {}) as Partial<BillingConfig>;
  return {
    ...DEFAULT_BILLING_CONFIG,
    ...c,
    mode: billingModeOf(c.mode),
    displayCurrency: c.displayCurrency === "CNY" ? "CNY" : "USD",
    usdToCNYRate: typeof c.usdToCNYRate === "number" ? c.usdToCNYRate : DEFAULT_BILLING_CONFIG.usdToCNYRate,
    nativeToolBillingEnabled: c.nativeToolBillingEnabled !== false,
    nativeToolPricing: Array.isArray(c.nativeToolPricing) ? c.nativeToolPricing : [],
    paymentProviders: Array.isArray(c.paymentProviders) ? c.paymentProviders : [],
    epayTypes: Array.isArray(c.epayTypes) ? c.epayTypes : [],
  };
}

export interface PlanPrice {
  id: number;
  planID: number;
  code: string;
  currency: string;
  billingInterval: string;
  amountCents: number;
  isDefault: boolean;
}

export interface BillingPlan {
  id: number;
  code: string;
  name: string;
  description: string;
  featureJSON: string;
  periodCreditUSD: number;
  periodCreditNanousd: number;
  sortOrder: number;
  isActive: boolean;
  permissionGroupID: number | null;
  prices: PlanPrice[];
}

export interface PermissionGroup {
  id: number;
  name: string;
  description: string;
  isDefault: boolean;
  rateMultiplierPercent: number;
  modelCount: number;
  manualModelCount: number;
  ruleModelCount: number;
  userCount: number;
  manualUserCount: number;
  subscriptionUserCount: number;
  createdAt: string;
  updatedAt: string;
}

export interface PlatformModel {
  id: number;
  platformModelName: string;
  vendor: string;
  vendorName: string;
  vendorIcon: string;
  displayGroupID: number | null;
  displayGroupName: string;
  displayGroupIcon: string;
  kindsJSON: string;
  icon: string;
  capabilitiesJSON: string;
  contextWindow: number;
  systemPrompt: string;
  accessScope: "public" | "internal" | (string & {});
  status: "active" | "inactive" | (string & {});
  description: string;
  cbPolicyMode: "default" | "enforced" | (string & {});
  cbFailureThreshold: number;
  cbDurationMin: number;
  cbWindowMin: number;
  sortOrder: number;
  sourceCount: number;
  activeSourceCount: number;
  protocolsJSON: string;
  upstreamNamesJSON: string;
  createdAt: string;
  updatedAt: string;
}

export interface ModelPrice {
  id: number;
  platformModelName: string;
  modelVendor: string;
  modelIcon: string;
  currency: string;
  isFree: boolean;
  pricingMode: "token" | "call" | "duration" | "tiered" | (string & {});
  inputUSDPerMTokens: number;
  cacheReadUSDPerMTokens: number;
  cacheWriteUSDPerMTokens: number;
  cacheWritePriceBasis?: string;
  outputUSDPerMTokens: number;
  callUSDPerCall: number;
  durationUSDPerSecond: number;
  tieredPricingJSON: string;
  schedulePricingJSON: string;
  createdAt: string;
  updatedAt: string;
}

export interface UserRecord {
  id: number;
  publicID: string;
  username: string;
  displayName: string;
  email: string;
  phone: string;
  avatarURL: string;
  role: "user" | "admin" | "superadmin" | (string & {});
  status: string;
  timezone: string;
  locale: string;
  profilePreferences: string;
  appearancePreferences: string;
  subscriptionTier: string;
  subscriptionPlanID?: number | null;
  subscriptionPlanName: string;
  subscriptionStatus: string;
  subscriptionExpiresAt: string | null;
  billingAccountStatus: string;
  billingAccountCurrency: string;
  billingBalanceUSD: number;
  billingBalanceNanousd: number;
  emailVerifiedAt: string | null;
  phoneVerifiedAt: string | null;
  lastLoginAt: string | null;
  lastActiveAt: string | null;
  createdAt: string;
  updatedAt: string;
  identityProviders: Array<string | { slug?: string; name?: string }>;
  twoFactorEnabled: boolean;
  twoFactorAvailable: boolean;
  twoFactorRequired: boolean;
  twoFactorRecoveryCount: number;
}
