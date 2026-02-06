/**
 * Request construction and envelope handling. Everything under `/api/v1`
 * answers with `{data, errorMsg, errorCode?, requestId?, details?}`; this
 * module turns that into either the unwrapped `data` or a typed ApiError.
 */
import { apiUrl } from "./base-url";
import { ApiError, NetworkError, isAbortError } from "./errors";
import { CLIENT_PLATFORM_HEADER, isDesktop } from "./platform";
import { isRecord } from "../utils/guards";

export type QueryValue = string | number | boolean | null | undefined | ReadonlyArray<string | number | boolean>;

export interface RequestOptions {
  method?: string;
  headers?: Record<string, string>;
  /** Strings and FormData go out as-is; anything else is JSON-encoded. */
  body?: unknown;
  query?: Record<string, QueryValue>;
  /** Bearer token. Set by the authenticated wrappers. */
  token?: string;
  signal?: AbortSignal;
  /** Lets a small request finish while the page unloads (<= 64 KB body). */
  keepalive?: boolean;
}

/** Percent-encodes one path segment. */
export function seg(value: string | number): string {
  return encodeURIComponent(String(value));
}

/** Serialises a query object; empty/undefined values are omitted, arrays repeat the key. */
export function toQuery(query: Record<string, QueryValue> | undefined): string {
  if (!query) return "";
  const params = new URLSearchParams();
  for (const [key, value] of Object.entries(query)) {
    if (value === undefined || value === null || value === "") continue;
    if (Array.isArray(value)) {
      for (const item of value) params.append(key, String(item));
    } else {
      params.append(key, String(value));
    }
  }
  const text = params.toString();
  return text ? `?${text}` : "";
}

let featureDisabledListener: (() => void) | null = null;

/** Capabilities registers here so a `feature.disabled` answer refreshes them. */
export function onFeatureDisabled(listener: (() => void) | null): void {
  featureDisabledListener = listener;
}

function buildInit(options: RequestOptions): RequestInit {
  const headers = new Headers(options.headers);
  if (isDesktop()) headers.set(CLIENT_PLATFORM_HEADER, "desktop");
  if (options.token) headers.set("Authorization", `Bearer ${options.token}`);

  let body: BodyInit | undefined;
  const raw = options.body;
  if (raw === undefined) body = undefined;
  else if (typeof raw === "string") body = raw;
  else if (typeof FormData !== "undefined" && raw instanceof FormData) body = raw; // boundary is generated
  else if (typeof Blob !== "undefined" && raw instanceof Blob) body = raw;
  else body = JSON.stringify(raw);

  if (typeof body === "string" && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (!headers.has("Accept")) headers.set("Accept", "application/json");

  return {
    method: options.method ?? "GET",
    headers,
    body,
    credentials: "include",
    cache: "no-store",
    keepalive: options.keepalive,
    signal: options.signal,
  };
}

/** Message normalisation: i18n keys pass through; auth statuses get stock keys. */
function normalizeMessage(message: string, status: number): string {
  const text = message.trim();
  if (/^errors\.[A-Za-z0-9_.]+$/.test(text)) return text;
  if (status === 401) return "errors.auth.unauthorized";
  if (status === 403) return "errors.auth.forbidden";
  return text;
}

function parseRetryAfter(value: string | null): number | undefined {
  if (!value) return undefined;
  const n = Number(value.trim());
  return Number.isInteger(n) && n > 0 ? n : undefined;
}

async function toApiError(response: Response): Promise<ApiError> {
  const text = await response.text().catch(() => "");
  let envelope: Record<string, unknown> | null = null;
  const type = response.headers.get("content-type") ?? "";
  if (type.includes("json") || text.trimStart().startsWith("{")) {
    try {
      const parsed: unknown = JSON.parse(text);
      if (isRecord(parsed)) envelope = parsed;
    } catch {
      /* non-JSON error body */
    }
  }
  const sent = typeof envelope?.errorMsg === "string" ? envelope.errorMsg : "";
  const rawMessage = sent || (envelope ? "" : text.trim().slice(0, 300)) || `Request failed (${response.status})`;
  const errorCode = typeof envelope?.errorCode === "string" ? envelope.errorCode : undefined;
  const error = new ApiError({
    message: normalizeMessage(rawMessage, response.status),
    status: response.status,
    errorCode,
    details: envelope?.details,
    requestId:
      (typeof envelope?.requestId === "string" && envelope.requestId) || response.headers.get("x-request-id") || undefined,
    retryAfterSeconds: parseRetryAfter(response.headers.get("retry-after")),
    rawMessage,
  });
  if (errorCode === "feature.disabled") featureDisabledListener?.();
  return error;
}

/**
 * Sends a request and returns the Response; non-OK statuses throw ApiError.
 * Use for streams and blobs. Aborts surface untouched, other fetch failures as
 * NetworkError.
 */
export async function rawRequest(path: string, options: RequestOptions = {}): Promise<Response> {
  const url = `${apiUrl(path)}${toQuery(options.query)}`;
  let response: Response;
  try {
    response = await fetch(url, buildInit(options));
  } catch (error) {
    if (isAbortError(error)) throw error;
    if (options.signal?.aborted) {
      const reason: unknown = options.signal.reason;
      throw reason instanceof Error ? reason : new DOMException("The operation was aborted.", "AbortError");
    }
    throw new NetworkError({ cause: error });
  }
  if (!response.ok) throw await toApiError(response);
  return response;
}

/** Sends a request and returns the envelope's `data`. */
export async function request<T = unknown>(path: string, options: RequestOptions = {}): Promise<T> {
  const response = await rawRequest(path, options);
  const type = response.headers.get("content-type") ?? "";
  if (response.status === 204) return undefined as T;
  if (!type.includes("json")) {
    throw new ApiError({ message: `Unexpected non-JSON response (${response.status})`, status: response.status });
  }
  let parsed: unknown;
  try {
    parsed = await response.json();
  } catch {
    throw new ApiError({ message: `Unexpected non-JSON response (${response.status})`, status: response.status });
  }
  const envelope = isRecord(parsed) ? parsed : {};
  const errorMsg = typeof envelope.errorMsg === "string" ? envelope.errorMsg : "";
  if (errorMsg) {
    throw new ApiError({
      message: normalizeMessage(errorMsg, response.status),
      status: response.status,
      errorCode: typeof envelope.errorCode === "string" ? envelope.errorCode : undefined,
      details: envelope.details,
      requestId: typeof envelope.requestId === "string" ? envelope.requestId : undefined,
      rawMessage: errorMsg,
    });
  }
  return envelope.data as T;
}
