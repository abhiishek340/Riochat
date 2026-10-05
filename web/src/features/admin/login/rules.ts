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
