/** Errors raised by the HTTP layer. UI code translates them via lib/i18n. */

export interface ApiErrorInit {
  message: string;
  status: number;
  errorCode?: string;
  details?: unknown;
  requestId?: string;
  retryAfterSeconds?: number;
  /** Message as the server sent it, before normalisation. */
  rawMessage?: string;
}

export class ApiError extends Error {
  readonly status: number;
  readonly errorCode?: string;
  readonly details?: unknown;
  readonly requestId?: string;
  readonly retryAfterSeconds?: number;
  readonly rawMessage: string;

  constructor(init: ApiErrorInit) {
    super(init.message);
    this.name = "ApiError";
    this.status = init.status;
    this.errorCode = init.errorCode;
    this.details = init.details;
    this.requestId = init.requestId;
    this.retryAfterSeconds = init.retryAfterSeconds;
    this.rawMessage = init.rawMessage ?? init.message;
  }
}

/** The request never produced an HTTP response (offline, DNS, refused...). */
export class NetworkError extends Error {
  constructor(options?: { cause?: unknown }) {
    super("errors.network.unavailable", options);
    this.name = "NetworkError";
  }
}

/** A streaming body broke the wire protocol. */
export class StreamProtocolError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "StreamProtocolError";
  }
}

export function isApiError(error: unknown): error is ApiError {
  return error instanceof ApiError;
}

export function isAbortError(error: unknown): boolean {
  return (
    typeof error === "object" &&
    error !== null &&
    (error as { name?: unknown }).name === "AbortError"
  );
}

/** i18n keys travel inside Error.message; `errors.auth.forbidden` is one. */
export function isMessageKey(message: string): boolean {
  return /^errors\.[A-Za-z0-9_.]+$/.test(message);
}

export type AuthErrorKind = "other" | "unauthorized" | "session_terminated";

/** Error codes with which the backend ends a session for good. */
export const SESSION_ENDING_CODES: readonly string[] = [
  "auth.invalid_token",
  "auth.invalid_refresh_token",
  "auth.session_invalid",
];

export function classifyAuthError(error: { status?: number; errorCode?: string } | null | undefined): AuthErrorKind {
  if (!error || error.status !== 401) return "other";
  if (error.errorCode && SESSION_ENDING_CODES.includes(error.errorCode)) return "session_terminated";
  return "unauthorized";
}
