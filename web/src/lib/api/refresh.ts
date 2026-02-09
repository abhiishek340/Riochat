/**
 * Access-token refresh as a small state machine, independent of fetch and of
 * the DOM so it can be unit tested and reused by other front ends.
 *
 * Guarantees:
 *  - concurrent refreshes from one client coalesce into one attempt;
 *  - a session replaced while a refresh ran is never clobbered by its failure;
 *  - a missing token or a session-ending 401 clears the session and resolves
 *    to "" rather than throwing; any other failure rethrows and keeps it;
 *  - inside the (optional) cross-tab lock the stored token is re-read, so a
 *    peer tab's fresh token is adopted without another network call;
 *  - a request is attempted at most twice.
 */
import { classifyAuthError, type AuthErrorKind } from "./errors";
import type { SessionSnapshot } from "./session-store";

export interface RefreshStore {
  token(): string;
  revision(): number;
  set(snapshot: SessionSnapshot, options?: { silent?: boolean }): void;
  clear(options?: { silent?: boolean }): void;
}

export interface RefreshDeps {
  store: RefreshStore;
  /** Yields fresh credentials, or null when there is no session. Throws on transport/HTTP failure. */
  transport: () => Promise<SessionSnapshot | null>;
  classify?: (error: unknown) => AuthErrorKind;
  /** Runs `fn` exclusively across tabs. Omit when no lock primitive exists. */
  lock?: <T>(fn: () => Promise<T>) => Promise<T>;
}

export interface AuthRetryOptions {
  /** Token to send first; defaults to the stored one. */
  token?: string;
  /** Refreshing is on by default. */
  allowRefresh?: boolean;
  /** True when `error` is the caller aborting; such errors skip all recovery. */
  isAbort?: (error: unknown) => boolean;
  /** Called after a token was recovered; throw here when the caller aborted meanwhile. */
  throwIfAborted?: () => void;
}

export interface Refresher {
  refresh(failedToken?: string): Promise<string>;
  recover(failedToken: string): Promise<string>;
  withAuthRetry<T>(run: (token: string) => Promise<T>, options?: AuthRetryOptions): Promise<T>;
}

function errorShape(error: unknown): { status?: number; errorCode?: string } {
  if (typeof error !== "object" || error === null) return {};
  const e = error as { status?: unknown; errorCode?: unknown };
  return {
    status: typeof e.status === "number" ? e.status : undefined,
    errorCode: typeof e.errorCode === "string" ? e.errorCode : undefined,
  };
}

export function createRefresher(deps: RefreshDeps): Refresher {
  const { store, transport } = deps;
  const classify = deps.classify ?? ((e: unknown) => classifyAuthError(errorShape(e)));
  let pending: Promise<string> | null = null;

  async function attempt(failedToken: string | undefined): Promise<string> {
    const startRevision = store.revision();
    const clearIfUntouched = () => {
      // A newer session appeared meanwhile (another tab, a fresh sign-in).
      if (store.revision() === startRevision) store.clear({ silent: true });
    };
    const body = async (): Promise<string> => {
      const stored = store.token();
      if (failedToken !== undefined && stored && stored !== failedToken) return stored;
      const creds = await transport();
      if (!creds || !creds.accessToken) {
        clearIfUntouched();
        return "";
      }
      store.set({ accessToken: creds.accessToken, sessionID: creds.sessionID });
      return creds.accessToken;
    };
    try {
      return await (deps.lock ? deps.lock(body) : body());
    } catch (error) {
      if (classify(error) === "session_terminated") {
        clearIfUntouched();
        return "";
      }
      throw error;
    }
  }

  function refresh(failedToken?: string): Promise<string> {
    if (pending) return pending;
    const run = attempt(failedToken);
    const tracked = run.finally(() => {
      if (pending === tracked) pending = null;
    });
    pending = tracked;
    return tracked;
  }

  async function recover(failedToken: string): Promise<string> {
    const stored = store.token();
    if (stored && stored !== failedToken) return stored;
    return refresh(failedToken);
  }

  async function withAuthRetry<T>(run: (token: string) => Promise<T>, options: AuthRetryOptions = {}): Promise<T> {
    const first = options.token ?? store.token();
    const startRevision = store.revision();
    try {
      return await run(first);
    } catch (error) {
      if (options.isAbort?.(error)) throw error;
      const kind = classify(error);
      if (kind === "session_terminated") {
        if (store.revision() === startRevision) store.clear({ silent: true });
        throw error;
      }
      if (options.allowRefresh === false || kind === "other") throw error;
      const next = await recover(first);
      options.throwIfAborted?.();
      if (!next) throw error;
      try {
        return await run(next);
      } catch (retryError) {
        if (!options.isAbort?.(retryError) && classify(retryError) === "session_terminated") store.clear({ silent: true });
        throw retryError;
      }
    }
  }

  return { refresh, recover, withAuthRetry };
}
