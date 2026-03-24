import { describe, expect, it } from "vitest";
import { ALL_ENABLED, disabledFeatureOf, readCapabilities } from "./features";

describe("capabilities", () => {
  it("turns flags off only on explicit booleans", () => {
    const caps = readCapabilities({ data: { features: { sharing: false, multiUser: "no", extra: true } } });
    expect(caps.sharing).toBe(false);
    expect(caps.multiUser).toBe(true);
    expect(Object.values(caps).filter((v) => !v)).toHaveLength(1);
  });
  it("is fully enabled with no usable information", () => {
    expect(readCapabilities(undefined)).toEqual(ALL_ENABLED);
    expect(readCapabilities({ features: 3 })).toEqual(ALL_ENABLED);
  });
  it("extracts only known features from feature.disabled errors", () => {
    expect(disabledFeatureOf({ errorCode: "feature.disabled", details: { feature: "sharing" } })).toBe("sharing");
    expect(disabledFeatureOf({ errorCode: "feature.disabled", details: { feature: "nope" } })).toBeUndefined();
    expect(disabledFeatureOf({ errorCode: "other", details: { feature: "sharing" } })).toBeUndefined();
  });
});
