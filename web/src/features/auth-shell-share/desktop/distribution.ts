/** What the shell says about how it was installed and what the organisation allows. */
import { isRecord } from "@/lib/utils/guards";
import { invoke } from "./bridge";

export type DistributionKind = "installer" | "msi" | "portable" | "dev";
export type UpdateMode = "auto" | "notify" | "disabled";

export interface DistributionPolicy {
  defaultServerUrl: string | null;
  serverUrlLocked: boolean;
  localModeAllowed: boolean;
  autoUpdateDisabledByPolicy: boolean;
  proxyManagedByPolicy: boolean;
}

export interface Distribution {
  kind: DistributionKind;
  updateMode: UpdateMode;
  releasesUrl: string;
  policy: DistributionPolicy;
}

export const FALLBACK_DISTRIBUTION: Distribution = {
  kind: "dev",
  updateMode: "disabled",
  releasesUrl: "",
  policy: { defaultServerUrl: null, serverUrlLocked: false, localModeAllowed: true, autoUpdateDisabledByPolicy: false, proxyManagedByPolicy: false },
};

const KINDS: readonly string[] = ["installer", "msi", "portable", "dev"];
const MODES: readonly string[] = ["auto", "notify", "disabled"];

/** Validates the payload; anything malformed yields null so the caller can fall back. */
export function readDistribution(raw: unknown): Distribution | null {
  if (!isRecord(raw) || !KINDS.includes(String(raw.kind)) || !MODES.includes(String(raw.updateMode)) || typeof raw.releasesUrl !== "string") return null;
  const policy = isRecord(raw.policy) ? raw.policy : null;
  if (!policy) return null;
  const flag = (key: string): boolean | null => (typeof policy[key] === "boolean" ? (policy[key] as boolean) : null);
  const locked = flag("serverUrlLocked");
  const local = flag("localModeAllowed");
  const auto = flag("autoUpdateDisabledByPolicy");
  const proxy = flag("proxyManagedByPolicy");
  const url = policy.defaultServerUrl;
  if (locked === null || local === null || auto === null || proxy === null) return null;
  if (url !== null && typeof url !== "string") return null;
  return {
    kind: raw.kind as DistributionKind,
    updateMode: raw.updateMode as UpdateMode,
    releasesUrl: raw.releasesUrl,
    policy: { defaultServerUrl: url, serverUrlLocked: locked, localModeAllowed: local, autoUpdateDisabledByPolicy: auto, proxyManagedByPolicy: proxy },
  };
}

let cached: Promise<Distribution> | null = null;

/** Cached for the page lifetime on success; failures are not cached. */
export function loadDistribution(): Promise<Distribution> {
  if (!cached) {
    cached = invoke("get_distribution").then(
      (raw) => {
        const parsed = readDistribution(raw);
        if (!parsed) cached = null;
        return parsed ?? FALLBACK_DISTRIBUTION;
      },
      () => {
        cached = null;
        return FALLBACK_DISTRIBUTION;
      },
    );
  }
  return cached;
}

export function resetDistributionCacheForTests(): void {
  cached = null;
}
