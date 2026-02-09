/**
 * The client every feature uses. Calls carry the in-memory access token; a 401
 * that does not end the session triggers one coalesced refresh and one retry.
 * `publicApi` skips authentication for endpoints that need none.
 */
import { isAbortError } from "./errors";
import { rawRequest, request, type RequestOptions } from "./http";
import { createRefresher, type Refresher } from "./refresh";
import { sessionStore, type SessionSnapshot } from "./session-store";
import { refreshTransport } from "./endpoints/refresh-transport";

const LOCK_NAME = "riochat:auth-refresh";

/** Cross-tab mutual exclusion through Web Locks; absent -> no lock. */
function webLock(): (<T>(fn: () => Promise<T>) => Promise<T>) | undefined {
  if (typeof navigator === "undefined" || !("locks" in navigator)) return undefined;
  return <T>(fn: () => Promise<T>) => navigator.locks.request(LOCK_NAME, fn) as Promise<T>;
}

export const refresher: Refresher = createRefresher({
  store: sessionStore,
  transport: (): Promise<SessionSnapshot | null> => refreshTransport(),
  lock: webLock(),
});

export type CallOptions = Omit<RequestOptions, "method" | "body" | "token"> & {
  /** Set false to surface the first 401 instead of refreshing. */
  refresh?: boolean;
};

function authed<T>(path: string, options: RequestOptions & { refresh?: boolean }): Promise<T> {
  const { refresh, ...rest } = options;
  return refresher.withAuthRetry((token) => request<T>(path, { ...rest, token }), {
    allowRefresh: refresh !== false,
    isAbort: isAbortError,
    throwIfAborted: () => rest.signal?.throwIfAborted(),
  });
}

function authedRaw(path: string, options: RequestOptions & { refresh?: boolean }): Promise<Response> {
  const { refresh, ...rest } = options;
  return refresher.withAuthRetry((token) => rawRequest(path, { ...rest, token }), {
    allowRefresh: refresh !== false,
    isAbort: isAbortError,
    throwIfAborted: () => rest.signal?.throwIfAborted(),
  });
}

export const api = {
  get: <T>(path: string, options: CallOptions = {}) => authed<T>(path, { ...options, method: "GET" }),
  post: <T>(path: string, body?: unknown, options: CallOptions = {}) => authed<T>(path, { ...options, method: "POST", body }),
  put: <T>(path: string, body?: unknown, options: CallOptions = {}) => authed<T>(path, { ...options, method: "PUT", body }),
  patch: <T>(path: string, body?: unknown, options: CallOptions = {}) => authed<T>(path, { ...options, method: "PATCH", body }),
  delete: <T>(path: string, options: CallOptions = {}) => authed<T>(path, { ...options, method: "DELETE" }),
  /** Authenticated fetch returning the Response (streams, blobs). */
  raw: (path: string, options: RequestOptions & { refresh?: boolean } = {}) => authedRaw(path, options),
};

export const publicApi = {
  get: <T>(path: string, options: CallOptions = {}) => request<T>(path, { ...options, method: "GET" }),
  post: <T>(path: string, body?: unknown, options: CallOptions = {}) => request<T>(path, { ...options, method: "POST", body }),
};

/**
 * Token for non-fetch consumers (image downloads, SSE): the cached one, else a
 * refresh, else "".
 */
export async function resolveAccessToken(): Promise<string> {
  const cached = sessionStore.token();
  if (cached) return cached;
  try {
    return await refresher.refresh();
  } catch {
    return "";
  }
}
