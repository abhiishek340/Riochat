import type { LoginOptions, LoginProvider } from "@/lib/api/types";

export type AuthMode = "login" | "register" | "reset";

/** What the form assumes until the server answers (or when it cannot). */
export const DEFAULT_LOGIN_OPTIONS: LoginOptions = {
  usernameEnabled: true,
  emailEnabled: true,
  emailRegistrationEnabled: true,
  emailVerificationEnabled: false,
  passwordResetEnabled: false,
  turnstileRegistrationEnabled: false,
  turnstileSiteKey: "",
  providerAuthBridge: { enabled: false, callbackBaseURL: "", protocolVersion: 0 },
  providers: [],
};

export interface AuthCapabilities {
  passwordLogin: boolean;
  /** Providers to show as buttons (bridge on, login enabled). */
  providers: LoginProvider[];
  emailRegistration: boolean;
  /** Registration is offered inside the app (not diverted to the browser). */
  registrationInApp: boolean;
  /** Desktop only: registration needs a widget the wrapper cannot show. */
  registerInBrowser: boolean;
  passwordReset: boolean;
  turnstileRequired: boolean;
  accountMode: "both" | "email" | "username";
}

export function deriveCapabilities(options: LoginOptions, env: { desktop: boolean }): AuthCapabilities {
  const passwordLogin = options.usernameEnabled || options.emailEnabled;
  const emailRegistration = options.emailEnabled && options.emailRegistrationEnabled;
  const turnstileRequired = options.turnstileRegistrationEnabled && options.turnstileSiteKey.trim() !== "";
  const registerInBrowser = env.desktop && emailRegistration && turnstileRequired;
  return {
    passwordLogin,
    providers: options.providerAuthBridge.enabled ? options.providers.filter((p) => p.loginEnabled) : [],
    emailRegistration,
    registrationInApp: emailRegistration && !registerInBrowser,
    registerInBrowser,
    passwordReset: passwordLogin && options.passwordResetEnabled,
    turnstileRequired,
    accountMode: options.emailEnabled && options.usernameEnabled ? "both" : options.emailEnabled ? "email" : "username",
  };
}

/** Moves to a mode the deployment actually offers. */
export function correctMode(mode: AuthMode, caps: AuthCapabilities): AuthMode {
  if (mode === "login" && !caps.passwordLogin && caps.providers.length === 0 && caps.registrationInApp) return "register";
  if (mode === "register" && !caps.registrationInApp) return "login";
  if (mode === "reset" && !caps.passwordReset) return "login";
  return mode;
}
