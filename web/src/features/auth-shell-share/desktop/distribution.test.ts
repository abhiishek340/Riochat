import { describe, expect, it } from "vitest";
import { FALLBACK_DISTRIBUTION, readDistribution } from "./distribution";

const valid = {
  kind: "portable",
  updateMode: "notify",
  releasesUrl: "https://example.com/releases",
  policy: { defaultServerUrl: null, serverUrlLocked: false, localModeAllowed: true, autoUpdateDisabledByPolicy: false, proxyManagedByPolicy: false },
};

describe("readDistribution", () => {
  it("accepts a well-formed payload", () => {
    expect(readDistribution(valid)).toEqual(valid);
    expect(readDistribution({ ...valid, policy: { ...valid.policy, defaultServerUrl: "https://chat.example.com", serverUrlLocked: true } })?.policy.serverUrlLocked).toBe(true);
  });
  it("rejects anything malformed so the caller can fall back", () => {
    expect(readDistribution(null)).toBeNull();
    expect(readDistribution({ ...valid, kind: "exe" })).toBeNull();
    expect(readDistribution({ ...valid, updateMode: "sometimes" })).toBeNull();
    expect(readDistribution({ ...valid, releasesUrl: 3 })).toBeNull();
    expect(readDistribution({ ...valid, policy: { ...valid.policy, localModeAllowed: "yes" } })).toBeNull();
    expect(readDistribution({ ...valid, policy: undefined })).toBeNull();
    expect(readDistribution({ ...valid, policy: { ...valid.policy, defaultServerUrl: 5 } })).toBeNull();
  });
  it("falls back to a development edition with updates off and local mode allowed", () => {
    expect(FALLBACK_DISTRIBUTION).toMatchObject({ kind: "dev", updateMode: "disabled", policy: { localModeAllowed: true, serverUrlLocked: false } });
  });
});
