import { describe, expect, it } from "vitest";
import type { LoginOptions } from "@/lib/api/types";
import { correctMode, DEFAULT_LOGIN_OPTIONS, deriveCapabilities } from "./login-options";

const base: LoginOptions = DEFAULT_LOGIN_OPTIONS;
const provider = { publicID: "p1", slug: "gh", name: "GitHub", type: "oauth2", logoURL: "", loginEnabled: true, registrationEnabled: true };

describe("deriveCapabilities", () => {
  it("defaults to password login with registration", () => {
    const caps = deriveCapabilities(base, { desktop: false });
    expect(caps.passwordLogin).toBe(true);
    expect(caps.emailRegistration).toBe(true);
    expect(caps.accountMode).toBe("both");
    expect(caps.passwordReset).toBe(false);
  });
  it("labels the account by which logins are enabled", () => {
    expect(deriveCapabilities({ ...base, usernameEnabled: false }, { desktop: false }).accountMode).toBe("email");
    expect(deriveCapabilities({ ...base, emailEnabled: false }, { desktop: false }).accountMode).toBe("username");
  });
  it("shows providers only when the bridge is on and login is enabled", () => {
    const off = deriveCapabilities({ ...base, providers: [provider] }, { desktop: false });
    expect(off.providers).toHaveLength(0);
    const on = deriveCapabilities(
      {
        ...base,
        providers: [provider, { ...provider, publicID: "p2", slug: "x", loginEnabled: false }],
        providerAuthBridge: { enabled: true, callbackBaseURL: "https://x", protocolVersion: 1 },
      },
      { desktop: false },
    );
    expect(on.providers.map((p) => p.slug)).toEqual(["gh"]);
  });
  it("requires a site key for human verification", () => {
    expect(deriveCapabilities({ ...base, turnstileRegistrationEnabled: true, turnstileSiteKey: "  " }, { desktop: false }).turnstileRequired).toBe(false);
    expect(deriveCapabilities({ ...base, turnstileRegistrationEnabled: true, turnstileSiteKey: "k" }, { desktop: false }).turnstileRequired).toBe(true);
  });
  it("sends desktop registration to the browser when verification is required", () => {
    const opts = { ...base, turnstileRegistrationEnabled: true, turnstileSiteKey: "k" };
    const desktop = deriveCapabilities(opts, { desktop: true });
    expect(desktop.registerInBrowser).toBe(true);
    expect(desktop.registrationInApp).toBe(false);
    expect(deriveCapabilities(opts, { desktop: false }).registrationInApp).toBe(true);
  });
  it("offers reset only with password login and the server flag", () => {
    expect(deriveCapabilities({ ...base, passwordResetEnabled: true }, { desktop: false }).passwordReset).toBe(true);
    expect(deriveCapabilities({ ...base, passwordResetEnabled: true, usernameEnabled: false, emailEnabled: false }, { desktop: false }).passwordReset).toBe(false);
  });
});

describe("correctMode", () => {
  it("switches to register when nothing else can sign in", () => {
    const caps = deriveCapabilities({ ...base, usernameEnabled: false, emailEnabled: true, emailRegistrationEnabled: true }, { desktop: false });
    expect(correctMode("login", caps)).toBe("login"); // email password login still exists
    const regOnly = { ...caps, passwordLogin: false, providers: [] };
    expect(correctMode("login", regOnly)).toBe("register");
  });
  it("leaves unavailable modes", () => {
    const caps = deriveCapabilities({ ...base, emailRegistrationEnabled: false }, { desktop: false });
    expect(correctMode("register", caps)).toBe("login");
    expect(correctMode("reset", caps)).toBe("login");
  });
});
