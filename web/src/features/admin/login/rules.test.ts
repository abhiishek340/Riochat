import { describe, expect, it } from "vitest";
import {
  A,
  applyFlag,
  blankProviderForm,
  callbackUrl,
  formFromProvider,
  formFromTemplate,
  loginChangeProblem,
  normalizeAuth,
  PROVIDER_TEMPLATES,
  providerBody,
  providerSlug,
  switchUrlMode,
  validateNextPath,
  validateProviderForm,
  validateTurnstile,
  validateVerification,
  withLoginEnabled,
} from "./rules";

const labels = (n: string) => n;

describe("normalizeAuth", () => {
  it("cascades email login off to registration and the human check", () => {
    const out = normalizeAuth({ [A.emailLogin]: "false", [A.emailRegistration]: "true", [A.turnstileOn]: "true" });
    expect(out[A.emailRegistration]).toBe("false");
    expect(out[A.turnstileOn]).toBe("false");
  });
  it("turns password reset off with verification", () => {
    expect(normalizeAuth({ [A.verification]: "false", [A.passwordReset]: "true" })[A.passwordReset]).toBe("false");
    expect(normalizeAuth({ [A.verification]: "true", [A.passwordReset]: "true" })[A.passwordReset]).toBe("true");
  });
  it("returns the same object when nothing changes", () => {
    const draft = { [A.emailLogin]: "true", [A.emailRegistration]: "true", [A.verification]: "true" };
    expect(normalizeAuth(draft)).toBe(draft);
  });
});

describe("login method guard", () => {
  it("refuses disabling the last password login when third-party login is off", () => {
    const draft = { [A.usernameLogin]: "false", [A.emailLogin]: "true", [A.thirdParty]: "false" };
    expect(loginChangeProblem(draft, A.emailLogin, "false")?.key).toBe("adminLogin.problems.cannotDisable");
  });
  it("allows it while third-party login stays on", () => {
    const draft = { [A.usernameLogin]: "false", [A.emailLogin]: "true", [A.thirdParty]: "true" };
    expect(loginChangeProblem(draft, A.emailLogin, "false")).toBeNull();
  });
  it("ignores switching things on or other settings", () => {
    expect(loginChangeProblem({ [A.thirdParty]: "false" }, A.usernameLogin, "true")).toBeNull();
    expect(loginChangeProblem({ [A.thirdParty]: "false" }, A.rateLimit, "false")).toBeNull();
  });
});

describe("group validation", () => {
  it("accepts only paths that start with a single slash", () => {
    expect(validateNextPath("/chat")).toBeNull();
    expect(validateNextPath("//evil.com")).not.toBeNull();
    expect(validateNextPath("chat")).not.toBeNull();
  });
  it("lists the missing SMTP values and checks the port", () => {
    const configured = new Set<string>();
    const out = validateVerification({ [A.verification]: "true", [A.smtpHost]: "", [A.smtpPort]: "587", [A.smtpUser]: "", [A.smtpPassword]: "" }, configured, labels);
    expect(out?.key).toBe("adminLogin.problems.smtpMissing");
    expect(out?.args).toEqual({ fields: "host, username, password" });
    const ok = { [A.verification]: "true", [A.smtpHost]: "mail.test", [A.smtpUser]: "u", [A.smtpPassword]: "" };
    expect(validateVerification({ ...ok, [A.smtpPort]: "70000" }, new Set([A.smtpPassword]), labels)?.key).toBe("adminLogin.problems.smtpPort");
    expect(validateVerification({ ...ok, [A.smtpPort]: "465" }, new Set([A.smtpPassword]), labels)).toBeNull();
  });
  it("skips verification checks while it is off", () => {
    expect(validateVerification({ [A.verification]: "false" }, new Set(), labels)).toBeNull();
  });
  it("needs registration and both keys for the human check", () => {
    const base = { [A.turnstileOn]: "true", [A.emailRegistration]: "true", [A.turnstileSite]: "s", [A.turnstileSecret]: "" };
    expect(validateTurnstile(base, new Set())?.key).toBe("adminLogin.problems.turnstileKeys");
    expect(validateTurnstile(base, new Set([A.turnstileSecret]))).toBeNull();
    expect(validateTurnstile({ ...base, [A.emailRegistration]: "false" }, new Set())?.key).toBe("adminLogin.problems.turnstileNeedsRegistration");
  });
});

describe("provider form", () => {
  it("starts from the defaults of the type", () => {
    expect(blankProviderForm("oidc").scopes).toBe("openid profile email");
    expect(blankProviderForm("oauth2").scopes).toBe("profile email");
  });
  it("layers a template without credentials", () => {
    const github = PROVIDER_TEMPLATES.find((t) => t.id === "github");
    const form = formFromTemplate(github!);
    expect(form).toMatchObject({ type: "oauth2", name: "GitHub", subjectField: "id", avatarField: "avatar_url", scopes: "read:user user:email", clientID: "", clientSecret: "" });
  });
  it("moves the URL across when the mode switches", () => {
    const form = { ...blankProviderForm("oidc"), urlMode: "issuer" as const, issuerURL: "https://id.test" };
    const next = switchUrlMode(form, "discovery");
    expect(next).toMatchObject({ urlMode: "discovery", discoveryURL: "https://id.test", issuerURL: "" });
  });
  it("keeps registration off whenever login is off", () => {
    expect(withLoginEnabled(blankProviderForm(), false).registrationEnabled).toBe(false);
    expect(applyFlag({ ...blankProviderForm(), loginEnabled: false, registrationEnabled: false }, "registration", true)).toEqual({ ok: false, reason: "loginFirst" });
    const off = applyFlag(blankProviderForm(), "login", false);
    expect(off.ok && off.form.registrationEnabled).toBe(false);
  });
  it("derives slugs and the callback address", () => {
    expect(providerSlug({ slug: "", name: "My IdP (Test)" })).toBe("my-idp--test");
    expect(providerSlug({ slug: "Custom_1", name: "x" })).toBe("custom_1");
    expect(providerSlug({ slug: "", name: "  " })).toBe("provider");
    expect(callbackUrl("https://api.test/", { slug: "", name: "Google" })).toBe("https://api.test/google/callback");
    expect(callbackUrl("", { slug: "", name: "Google" })).toBeNull();
  });
  it("validates required values", () => {
    const form = { ...blankProviderForm("oauth2"), name: "X", clientID: "id", clientSecret: "s" };
    expect(validateProviderForm(form, false)).toBe("oauthUrls");
    expect(validateProviderForm({ ...form, authURL: "a", tokenURL: "t", userinfoURL: "u" }, false)).toBeNull();
    expect(validateProviderForm({ ...form, clientSecret: "", authURL: "a", tokenURL: "t", userinfoURL: "u" }, false)).toBe("clientSecret");
    expect(validateProviderForm({ ...form, clientSecret: "", authURL: "a", tokenURL: "t", userinfoURL: "u" }, true)).toBeNull();
    expect(validateProviderForm({ ...blankProviderForm("oidc"), name: "X", clientID: "id", clientSecret: "s" }, false)).toBe("issuer");
  });
  it("omits a blank secret so an edit keeps the stored one", () => {
    const body = providerBody({ ...blankProviderForm("oidc"), name: " A ", clientID: "id", urlMode: "issuer", issuerURL: "https://i.test", discoveryURL: "ignored" });
    expect(body.clientSecret).toBeUndefined();
    expect(body).toMatchObject({ name: "A", issuerURL: "https://i.test", discoveryURL: "" });
  });
  it("round-trips a provider view", () => {
    const form = formFromProvider({ publicID: "p1", type: "oidc", name: "N", slug: "n", logoURL: "", loginEnabled: true, registrationEnabled: false, clientID: "cid", discoveryURL: "https://d.test", scopes: "", defaultRole: "", subjectField: "", emailField: "", emailVerifiedField: "", nameField: "", avatarField: "" });
    expect(form).toMatchObject({ urlMode: "discovery", discoveryURL: "https://d.test", scopes: "openid profile email", defaultRole: "user", subjectField: "sub", registrationEnabled: false });
  });
});
