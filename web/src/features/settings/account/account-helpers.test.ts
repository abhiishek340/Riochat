import { describe, expect, it } from "vitest";
import type { LoginProvider } from "@/lib/api/types";
import { bindableProviders } from "./identities-section";
import { deviceTitle, locationText } from "./sessions-section";
import { secondsUntil } from "./two-factor-dialog";
import { challengeFor } from "./bind-flow";
import type { IdentityRow } from "./api";

const provider = (slug: string, loginEnabled = true): LoginProvider => ({ publicID: slug, slug, name: slug, type: "oidc", logoURL: "", loginEnabled, registrationEnabled: false });

describe("bindableProviders", () => {
  it("offers login-enabled providers that are not linked yet", () => {
    const linked = [{ providerSlug: "github" }] as IdentityRow[];
    expect(bindableProviders([provider("github"), provider("gitlab"), provider("off", false)], linked).map((p) => p.slug)).toEqual(["gitlab"]);
  });
});

describe("sessions", () => {
  it("titles a device by browser and system, else label, else name", () => {
    expect(deviceTitle({ browserName: "Firefox", osName: "Linux", deviceLabel: "x", deviceName: "y" }, "?")).toBe("Firefox (Linux)");
    expect(deviceTitle({ browserName: "Firefox", osName: "", deviceLabel: "Laptop", deviceName: "y" }, "?")).toBe("Laptop");
    expect(deviceTitle({ browserName: "", osName: "", deviceLabel: "", deviceName: "Phone" }, "?")).toBe("Phone");
    expect(deviceTitle({ browserName: "", osName: "", deviceLabel: "", deviceName: "" }, "Unknown device")).toBe("Unknown device");
  });
  it("joins city, region and country", () => {
    expect(locationText({ cityName: "Oslo", regionName: "", countryCode: "NO" })).toBe("Oslo, NO");
    expect(locationText({ cityName: "", regionName: "", countryCode: "" })).toBe("");
  });
});

describe("two-factor expiry", () => {
  it("counts seconds to the expiry time", () => {
    const now = Date.parse("2025-01-01T00:00:00Z");
    expect(secondsUntil("2025-01-01T00:05:00Z", now)).toBe(300);
    expect(secondsUntil("2024-12-31T23:59:00Z", now)).toBeLessThan(0);
    expect(secondsUntil("garbage", now)).toBe(0);
  });
});

describe("PKCE challenge", () => {
  it("matches the RFC 7636 example", async () => {
    expect(await challengeFor("dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk")).toBe("E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM");
  });
});
