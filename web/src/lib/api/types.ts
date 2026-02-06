/** Wire types for the endpoints the shared layer itself calls. */

export interface Envelope<T = unknown> {
  data: T;
  errorMsg: string;
  errorCode?: string;
  requestId?: string;
  details?: unknown;
}

export interface Paged<T> {
  results: T[];
  total: number;
}

export type UserRole = "user" | "admin" | "superadmin" | (string & {});

export interface AuthUser {
  id: number;
  publicID: string;
  username: string;
  displayName: string;
  avatarURL: string;
  email: string;
  phone: string;
  role: UserRole;
  status: string;
  timezone: string;
  locale: string;
  profilePreferences: string;
  appearancePreferences: string;
  onboardingCompletedAt: string | null;
  emailVerifiedAt: string | null;
  passwordEnabled: boolean;
  mustResetPassword: boolean;
  initialUsernameRequired: boolean;
  initialSecurityRequired: boolean;
  twoFactorAvailable: boolean;
  twoFactorEnabled: boolean;
  twoFactorRequired: boolean;
  subscriptionTier: string;
  subscriptionPlanName: string;
  subscriptionStatus: string;
  createdAt: string;
  updatedAt: string;
  [extra: string]: unknown;
}

export interface AuthPayload {
  accessToken: string;
  sessionID: string;
  expiresAt: string;
  refreshExpiresAt: string;
  refreshToken?: string;
  user: AuthUser;
  twoFactorRequired: boolean;
  twoFactorChallengeToken?: string;
  verificationMethods?: string[];
}

export interface Branding {
  title: string;
  shortName: string;
  description: string;
  logoURL: string;
  faviconURL: string;
  pwaIcon192URL: string;
  pwaIcon512URL: string;
  pwaMaskableIcon512URL: string;
  appleTouchIcon180URL: string;
}

export interface LoginProvider {
  publicID: string;
  slug: string;
  name: string;
  type: "oidc" | "oauth2" | (string & {});
  logoURL: string;
  loginEnabled: boolean;
  registrationEnabled: boolean;
}

export interface LoginOptions {
  usernameEnabled: boolean;
  emailEnabled: boolean;
  emailRegistrationEnabled: boolean;
  emailVerificationEnabled: boolean;
  passwordResetEnabled: boolean;
  turnstileRegistrationEnabled: boolean;
  turnstileSiteKey: string;
  providerAuthBridge: { enabled: boolean; callbackBaseURL: string; protocolVersion: number };
  providers: LoginProvider[];
}

export interface VersionInfo {
  product?: string;
  version?: string;
  commit?: string;
  buildTime?: string;
  buildID?: string;
}

export interface FeaturePolicy {
  knowledgeBaseEnabled: boolean;
  processTraceEnabled: boolean;
  desktopDownloadEnabled: boolean;
  desktopDownloadURL: string;
}
