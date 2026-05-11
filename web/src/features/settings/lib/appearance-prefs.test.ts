import { describe, expect, it } from "vitest";
import {
  accentFromAccountPreset,
  ACCOUNT_PRESET_OF,
  parseAppearanceBundle,
  resolveAppearance,
  serializeAppearanceBundle,
  type FullAppearance,
} from "./appearance-prefs";

const device: FullAppearance = { preset: "tide", chatFont: "default", chatFontWeight: "regular", fontSize: "standard" };

describe("appearance bundle", () => {
  it("parses leniently", () => {
    expect(parseAppearanceBundle("")).toEqual({});
    expect(parseAppearanceBundle("not json")).toEqual({});
    expect(parseAppearanceBundle("[1]")).toEqual({});
    expect(parseAppearanceBundle('"x"')).toEqual({});
    // A valid preset is kept while the bogus font size is ignored.
    expect(parseAppearanceBundle('{"preset":"sepia","fontSize":"bogus"}')).toEqual({ preset: "ember" });
    expect(parseAppearanceBundle('{"chatFont":"mono","chatFontWeight":"bold","fontSize":"large"}')).toEqual({
      chatFont: "mono",
      chatFontWeight: "bold",
      fontSize: "large",
    });
  });
  it("always serialises all four keys with server preset names", () => {
    const text = serializeAppearanceBundle({ ...device, fontSize: "large" });
    expect(JSON.parse(text)).toEqual({ preset: "azure", chatFont: "default", chatFontWeight: "regular", fontSize: "large" });
  });
  it("round-trips every accent", () => {
    for (const accent of ["tide", "ember", "moss", "dusk"] as const) {
      const text = serializeAppearanceBundle({ ...device, preset: accent });
      expect(parseAppearanceBundle(text).preset).toBe(accent);
      expect(ACCOUNT_PRESET_OF[accent]).toBeTruthy();
    }
  });
  it("maps every server preset name to some accent", () => {
    for (const name of ["default", "azure", "cobalt", "graphite", "lagoon", "ink", "ochre", "sepia"]) {
      expect(accentFromAccountPreset(name)).toBeDefined();
    }
    expect(accentFromAccountPreset("neon")).toBeUndefined();
    expect(accentFromAccountPreset(3)).toBeUndefined();
  });
  it("lets account values win only for the keys they contain", () => {
    expect(resolveAppearance(device, { fontSize: "small" })).toEqual({ ...device, fontSize: "small" });
    expect(resolveAppearance(device, {})).toEqual(device);
  });
});
