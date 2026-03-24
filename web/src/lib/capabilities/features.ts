/**
 * Server feature flags. With no information every flag is on: a flag only
 * turns off when the server says so explicitly, so older servers and failed
 * requests never hide functionality.
 */
import { isRecord } from "../utils/guards";

export const FEATURE_NAMES = [
  "multiUser",
  "registration",
  "identityProviders",
  "accountSecurity",
  "announcements",
  "billingGating",
  "usageMetering",
  "contentModeration",
  "sharing",
  "desktopDownload",
] as const;

export type FeatureName = (typeof FEATURE_NAMES)[number];
export type Capabilities = Readonly<Record<FeatureName, boolean>>;

export const ALL_ENABLED: Capabilities = Object.freeze(
  Object.fromEntries(FEATURE_NAMES.map((n) => [n, true])) as Record<FeatureName, boolean>,
);

/** Accepts `{features}` or the full envelope `{data: {features}}`. */
export function readCapabilities(payload: unknown): Capabilities {
  const root = isRecord(payload) && isRecord(payload.data) ? payload.data : payload;
  const features = isRecord(root) && isRecord(root.features) ? root.features : null;
  if (!features) return ALL_ENABLED;
  const flags = {} as Record<FeatureName, boolean>;
  for (const name of FEATURE_NAMES) {
    flags[name] = typeof features[name] === "boolean" ? (features[name] as boolean) : true;
  }
  return Object.freeze(flags);
}

export function isFeatureName(value: unknown): value is FeatureName {
  return typeof value === "string" && (FEATURE_NAMES as readonly string[]).includes(value);
}

export const FEATURE_DISABLED_CODE = "feature.disabled";

/** The feature named by a `feature.disabled` API error, if it is one we know. */
export function disabledFeatureOf(error: { errorCode?: string; details?: unknown } | null | undefined): FeatureName | undefined {
  if (!error || error.errorCode !== FEATURE_DISABLED_CODE) return undefined;
  const details = error.details;
  if (!isRecord(details)) return undefined;
  return isFeatureName(details.feature) ? details.feature : undefined;
}
