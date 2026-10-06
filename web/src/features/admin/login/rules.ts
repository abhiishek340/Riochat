/** Pure rules of the sign-in screen: setting cascades, group validation and the provider form. */
import { problem, type Problem } from "../lib/problem";
import { fieldId, hasValue, isDigits, type Draft } from "../lib/settings-form";

const id = (key: string) => fieldId({ ns: "auth", key });
export const A = {
  nextPath: id("login_default_next_path"),
  emailLogin: id("email_login_enabled"),
  emailRegistration: id("email_registration_enabled"),
  passwordReset: id("password_reset_enabled"),
  usernameLogin: id("username_login_enabled"),
  thirdParty: id("third_party_login_enabled"),
  turnstileOn: id("turnstile_registration_enabled"),
  turnstileSite: id("turnstile_site_key"),
  turnstileSecret: id("turnstile_secret_key"),
  verification: id("email_verification_enabled"),
  smtpHost: id("smtp_host"),
  smtpPort: id("smtp_port"),
  smtpUser: id("smtp_username"),
  smtpPassword: id("smtp_password"),
  smtpFrom: id("smtp_from"),
  rateLimit: id("rate_limit_enabled"),
} as const;

const isOn = (draft: Draft, key: string, fallback: boolean) => (draft[key] === undefined || draft[key] === "" ? fallback : draft[key] === "true");

/**
 * Dependent switches: email login off forces email registration off, which
 * forces the human check off; verification off forces password reset off.
 * Applied after every edit and to values read from the server.
 */
export function normalizeAuth(next: Draft): Draft {
  let out = next;
  const set = (key: string, value: string) => {
    if (out[key] !== value) out = { ...out, [key]: value };
  };
  if (!isOn(out, A.emailLogin, true)) set(A.emailRegistration, "false");
  if (!isOn(out, A.emailRegistration, true)) set(A.turnstileOn, "false");
  if (!isOn(out, A.verification, false)) set(A.passwordReset, "false");
  return out;
}

/** True when no way to sign in remains: both password logins and third-party login are off. */
export function noLoginMethodLeft(draft: Draft): boolean {
  return !isOn(draft, A.usernameLogin, true) && !isOn(draft, A.emailLogin, true) && !isOn(draft, A.thirdParty, true);
}

/**
 * Refuses turning the last password login off while third-party login is off
 * too (someone would have to be bound by an administrator first).
 */
export function loginChangeProblem(draft: Draft, changedId: string, value: string): Problem | null {
  if ((changedId === A.usernameLogin || changedId === A.emailLogin) && value === "false") {
    if (noLoginMethodLeft({ ...draft, [changedId]: value })) return problem("adminLogin.problems.cannotDisable");
  }
  return null;
}

export function validateNextPath(value: string): Problem | null {
  const v = value.trim();
  return v.startsWith("/") && !v.startsWith("//") ? null : problem("adminLogin.problems.nextPath");
}

export function validateLoginMethods(draft: Draft): Problem | null {
  return noLoginMethodLeft(draft) ? problem("adminLogin.problems.cannotDisable") : null;
}

export function validateTurnstile(draft: Draft, configured: ReadonlySet<string>): Problem | null {
  if (!isOn(draft, A.turnstileOn, false)) return null;
  if (!isOn(draft, A.emailRegistration, true)) return problem("adminLogin.problems.turnstileNeedsRegistration");
  if (!hasValue(draft, configured, A.turnstileSite) || !hasValue(draft, configured, A.turnstileSecret)) return problem("adminLogin.problems.turnstileKeys");
  return null;
}

/** Labels (catalog keys) of the SMTP values that are still missing. */
export function missingSmtp(draft: Draft, configured: ReadonlySet<string>): Array<"host" | "port" | "username" | "password"> {
  const out: Array<"host" | "port" | "username" | "password"> = [];
  if (!hasValue(draft, configured, A.smtpHost)) out.push("host");
  if (!hasValue(draft, configured, A.smtpPort)) out.push("port");
  if (!hasValue(draft, configured, A.smtpUser)) out.push("username");
  if (!hasValue(draft, configured, A.smtpPassword)) out.push("password");
  return out;
}

export function validateVerification(draft: Draft, configured: ReadonlySet<string>, label: (name: "host" | "port" | "username" | "password") => string): Problem | null {
  if (!isOn(draft, A.verification, false)) return null;
  const missing = missingSmtp(draft, configured);
  if (missing.length > 0) return problem("adminLogin.problems.smtpMissing", { fields: missing.map(label).join(", ") });
  const port = (draft[A.smtpPort] ?? "").trim();
  if (port !== "" && (!isDigits(port) || Number(port) < 1 || Number(port) > 65535)) return problem("adminLogin.problems.smtpPort");
  return null;
}

// --- identity providers -----------------------------------------------------

export interface IdentityProvider {
  publicID: string;
  type: "oidc" | "oauth2" | (string & {});
  name: string;
  slug: string;
  logoURL: string;
  loginEnabled: boolean;
  registrationEnabled: boolean;
  clientID?: string;
  issuerURL?: string;
  discoveryURL?: string;
  authURL?: string;
  tokenURL?: string;
  userinfoURL?: string;
  jwksURL?: string;
  scopes: string;
  defaultRole: string;
  subjectField: string;
  emailField: string;
  emailVerifiedField: string;
  nameField: string;
  avatarField: string;
}

export interface ProviderForm {
  type: "oidc" | "oauth2";
  name: string;
  slug: string;
  logoURL: string;
  loginEnabled: boolean;
  registrationEnabled: boolean;
  clientID: string;
  clientSecret: string;
  urlMode: "issuer" | "discovery";
  issuerURL: string;
  discoveryURL: string;
  authURL: string;
  tokenURL: string;
  userinfoURL: string;
  jwksURL: string;
  scopes: string;
  defaultRole: string;
  subjectField: string;
  emailField: string;
  emailVerifiedField: string;
  nameField: string;
  avatarField: string;
}

export const OIDC_SCOPES = "openid profile email";
export const OAUTH2_SCOPES = "profile email";

export function blankProviderForm(type: "oidc" | "oauth2" = "oidc"): ProviderForm {
  return {
    type,
    name: "",
    slug: "",
    logoURL: "",
    loginEnabled: true,
    registrationEnabled: true,
    clientID: "",
    clientSecret: "",
    urlMode: "discovery",
    issuerURL: "",
    discoveryURL: "",
    authURL: "",
    tokenURL: "",
    userinfoURL: "",
    jwksURL: "",
    scopes: type === "oidc" ? OIDC_SCOPES : OAUTH2_SCOPES,
    defaultRole: "user",
    subjectField: "sub",
    emailField: "email",
    emailVerifiedField: "email_verified",
    nameField: "name",
    avatarField: "picture",
  };
}

export interface ProviderTemplate {
  id: string;
  name: string;
  /** Fields layered over the blank form for its type. Client id and secret are never prefilled. */
  fill: Partial<ProviderForm> & { type: "oidc" | "oauth2" };
}

export const PROVIDER_TEMPLATES: readonly ProviderTemplate[] = [
  { id: "apple", name: "Apple", fill: { type: "oidc", urlMode: "issuer", issuerURL: "https://appleid.apple.com", scopes: "openid email name" } },
  {
    id: "github",
    name: "GitHub",
    fill: {
      type: "oauth2",
      authURL: "https://github.com/login/oauth/authorize",
      tokenURL: "https://github.com/login/oauth/access_token",
      userinfoURL: "https://api.github.com/user",
      scopes: "read:user user:email",
      subjectField: "id",
      avatarField: "avatar_url",
    },
  },
  { id: "google", name: "Google", fill: { type: "oidc", urlMode: "discovery", discoveryURL: "https://accounts.google.com/.well-known/openid-configuration" } },
  {
    id: "discord",
    name: "Discord",
    fill: {
      type: "oauth2",
      authURL: "https://discord.com/oauth2/authorize",
      tokenURL: "https://discord.com/api/oauth2/token",
      userinfoURL: "https://discord.com/api/users/@me",
      scopes: "identify email",
      subjectField: "id",
      emailVerifiedField: "verified",
      nameField: "username",
      avatarField: "avatar",
    },
  },
  {
    id: "facebook",
    name: "Facebook",
    fill: {
      type: "oauth2",
      authURL: "https://www.facebook.com/dialog/oauth",
      tokenURL: "https://graph.facebook.com/oauth/access_token",
      userinfoURL: "https://graph.facebook.com/me?fields=id,name,email,picture",
      scopes: "email public_profile",
      subjectField: "id",
      avatarField: "picture",
    },
  },
  { id: "huggingface", name: "Hugging Face", fill: { type: "oidc", urlMode: "discovery", discoveryURL: "https://huggingface.co/.well-known/openid-configuration" } },
  {
    id: "linuxdo",
    name: "Linux Do",
    fill: {
      type: "oauth2",
      authURL: "https://connect.linux.do/oauth2/authorize",
      tokenURL: "https://connect.linux.do/oauth2/token",
      userinfoURL: "https://connect.linux.do/api/user",
      scopes: "profile email",
      subjectField: "id",
      nameField: "username",
      avatarField: "avatar_url",
    },
  },
  { id: "microsoft", name: "Microsoft", fill: { type: "oidc", urlMode: "discovery", discoveryURL: "https://login.microsoftonline.com/common/v2.0/.well-known/openid-configuration" } },
  {
    id: "twitter",
    name: "Twitter (X)",
    fill: {
      type: "oauth2",
      authURL: "https://twitter.com/i/oauth2/authorize",
      tokenURL: "https://api.twitter.com/2/oauth2/token",
      userinfoURL: "https://api.twitter.com/2/users/me?user.fields=profile_image_url",
      scopes: "users.read tweet.read offline.access",
      subjectField: "id",
      avatarField: "profile_image_url",
    },
  },
  { id: "vercel", name: "Vercel", fill: { type: "oidc", urlMode: "discovery", discoveryURL: "https://vercel.com/.well-known/openid-configuration" } },
];

/** A template layered over the blank form of its type; the name is prefilled. */
export function formFromTemplate(template: ProviderTemplate): ProviderForm {
  return { ...blankProviderForm(template.fill.type), ...template.fill, name: template.name };
}

export function formFromProvider(p: IdentityProvider): ProviderForm {
  const type = p.type === "oauth2" ? "oauth2" : "oidc";
  const base = blankProviderForm(type);
  return {
    ...base,
    type,
    name: p.name,
    slug: p.slug,
    logoURL: p.logoURL ?? "",
    loginEnabled: p.loginEnabled,
    registrationEnabled: p.registrationEnabled,
    clientID: p.clientID ?? "",
    urlMode: p.issuerURL ? "issuer" : "discovery",
    issuerURL: p.issuerURL ?? "",
    discoveryURL: p.discoveryURL ?? "",
    authURL: p.authURL ?? "",
    tokenURL: p.tokenURL ?? "",
    userinfoURL: p.userinfoURL ?? "",
    jwksURL: p.jwksURL ?? "",
    scopes: p.scopes || base.scopes,
    defaultRole: p.defaultRole || "user",
    subjectField: p.subjectField || base.subjectField,
    emailField: p.emailField || base.emailField,
    emailVerifiedField: p.emailVerifiedField || base.emailVerifiedField,
    nameField: p.nameField || base.nameField,
    avatarField: p.avatarField || base.avatarField,
  };
}

/** Switching the URL mode moves the value across and blanks the other. */
export function switchUrlMode(form: ProviderForm, mode: "issuer" | "discovery"): ProviderForm {
  if (mode === form.urlMode) return form;
  const value = form.urlMode === "issuer" ? form.issuerURL : form.discoveryURL;
  return { ...form, urlMode: mode, issuerURL: mode === "issuer" ? value : "", discoveryURL: mode === "discovery" ? value : "" };
}

/** Changing the type resets what depends on it (scopes, URL fields). */
export function switchType(form: ProviderForm, type: "oidc" | "oauth2"): ProviderForm {
  if (type === form.type) return form;
  return { ...form, type, scopes: type === "oidc" ? OIDC_SCOPES : OAUTH2_SCOPES, issuerURL: "", discoveryURL: "", authURL: "", tokenURL: "", userinfoURL: "", jwksURL: "" };
}

/** Registration can only be on while login is on. */
export function withLoginEnabled(form: ProviderForm, on: boolean): ProviderForm {
  return { ...form, loginEnabled: on, registrationEnabled: on ? form.registrationEnabled : false };
}

export function slugify(name: string): string {
  return name
    .trim()
    .toLowerCase()
    .replace(/\s+/g, "-")
    .replace(/[^a-z0-9_-]/g, "-")
    .replace(/^-+|-+$/g, "");
}

/** Explicit slug, else derived from the name, else "provider". */
export function providerSlug(form: Pick<ProviderForm, "slug" | "name">): string {
  return slugify(form.slug) || slugify(form.name) || "provider";
}

export function callbackUrl(baseUrl: string | undefined | null, form: Pick<ProviderForm, "slug" | "name">): string | null {
  const base = (baseUrl ?? "").trim().replace(/\/+$/, "");
  return base ? `${base}/${providerSlug(form)}/callback` : null;
}

export type ProviderProblem = "clientId" | "clientSecret" | "oauthUrls" | "name" | "issuer";

export function validateProviderForm(form: ProviderForm, editing: boolean): ProviderProblem | null {
  if (!form.name.trim()) return "name";
  if (!form.clientID.trim()) return "clientId";
  if (!editing && !form.clientSecret.trim()) return "clientSecret";
  if (form.type === "oauth2" && (!form.authURL.trim() || !form.tokenURL.trim() || !form.userinfoURL.trim())) return "oauthUrls";
  if (form.type === "oidc" && !form.issuerURL.trim() && !form.discoveryURL.trim()) return "issuer";
  return null;
}

/** Request body. A blank secret is omitted so an edit keeps the stored one. */
export function providerBody(form: ProviderForm): Record<string, unknown> {
  const body: Record<string, unknown> = {
    type: form.type,
    name: form.name.trim(),
    slug: form.slug.trim(),
    logoURL: form.logoURL.trim(),
    loginEnabled: form.loginEnabled,
    registrationEnabled: form.loginEnabled && form.registrationEnabled,
    clientID: form.clientID.trim(),
    scopes: form.scopes.trim(),
    defaultRole: form.defaultRole || "user",
    subjectField: form.subjectField.trim(),
    emailField: form.emailField.trim(),
    emailVerifiedField: form.emailVerifiedField.trim(),
    nameField: form.nameField.trim(),
    avatarField: form.avatarField.trim(),
  };
  if (form.clientSecret.trim()) body.clientSecret = form.clientSecret.trim();
  if (form.type === "oidc") {
    body.issuerURL = form.urlMode === "issuer" ? form.issuerURL.trim() : "";
    body.discoveryURL = form.urlMode === "discovery" ? form.discoveryURL.trim() : "";
    body.jwksURL = form.jwksURL.trim();
  } else {
    body.authURL = form.authURL.trim();
    body.tokenURL = form.tokenURL.trim();
    body.userinfoURL = form.userinfoURL.trim();
    body.jwksURL = form.jwksURL.trim();
  }
  return body;
}

export type FlagChange = { ok: true; form: ProviderForm } | { ok: false; reason: "loginFirst" };

/** Inline switch rules: registration needs login; turning login off clears registration. */
export function applyFlag(form: ProviderForm, flag: "login" | "registration", on: boolean): FlagChange {
  if (flag === "registration") {
    if (on && !form.loginEnabled) return { ok: false, reason: "loginFirst" };
    return { ok: true, form: { ...form, registrationEnabled: on } };
  }
  return { ok: true, form: withLoginEnabled(form, on) };
}
