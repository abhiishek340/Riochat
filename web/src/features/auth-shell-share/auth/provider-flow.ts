/**
 * Starts the identity-provider hand-off (authorization code + PKCE through the
 * server-side bridge). The browser leaves the page; the desktop shell opens the
 * system browser and waits for its loopback listener to report back.
 */
import { isAbortError } from "@/lib/api/errors";
import { isDesktop } from "@/lib/api/platform";
import { invoke, listen, openExternal, shell } from "../desktop/bridge";
import { dropBridgeRequest, saveBridgeRequest, type BridgeIntent } from "../lib/handoff-storage";
import { createPkcePair } from "../lib/pkce";
import { authorizeProvider, CLIENT_ID_DESKTOP, CLIENT_ID_WEB } from "./api";

export interface StartProviderInput {
  slug: string;
  intent: BridgeIntent;
  /** Where to land after the exchange (same-origin path). */
  next: string;
  /** Client-side navigation, used by the desktop path to reach /auth/callback. */
  navigate: (path: string) => void;
  signal?: AbortSignal;
}

/** `<origin>/auth/callback?provider=<slug>` for browsers. */
export function webRedirectUri(origin: string, slug: string): string {
  return `${origin}/auth/callback?provider=${encodeURIComponent(slug)}`;
}

/** Turns the loopback URL the shell reports into the in-app callback route. */
export function callbackPathFromLoopback(url: string): string | null {
  try {
    const parsed = new URL(url);
    if (parsed.hostname !== "127.0.0.1" && parsed.hostname !== "localhost") return null;
    return `/auth/callback${parsed.search}`;
  } catch {
    return null;
  }
}

function waitForLoopback(signal?: AbortSignal): Promise<string> {
  return new Promise<string>((resolve, reject) => {
    let off: (() => void) | undefined;
    let done = false;
    const finish = (action: () => void) => {
      if (done) return;
      done = true;
      off?.();
      signal?.removeEventListener("abort", onAbort);
      action();
    };
    const onAbort = () => finish(() => reject(new DOMException("aborted", "AbortError")));
    if (signal?.aborted) {
      onAbort();
      return;
    }
    signal?.addEventListener("abort", onAbort);
    listen("oauth-loopback-callback", (payload) => {
      if (typeof payload === "string") finish(() => resolve(payload));
    }).then(
      (unlisten) => {
        off = unlisten;
        if (done) unlisten();
      },
      (error: unknown) => finish(() => reject(error)),
    );
  });
}

export async function startProviderFlow(input: StartProviderInput): Promise<void> {
  const desktop = isDesktop();
  const pkce = await createPkcePair();
  let redirectURI: string;
  let loopbackStarted = false;
  if (desktop) {
    redirectURI = await shell.startOauthLoopback();
    loopbackStarted = true;
  } else {
    redirectURI = webRedirectUri(window.location.origin, input.slug);
  }
  saveBridgeRequest(input.slug, { verifier: pkce.verifier, state: pkce.state, intent: input.intent, next: input.next });
  try {
    const url = await authorizeProvider(
      input.slug,
      {
        clientID: desktop ? CLIENT_ID_DESKTOP : CLIENT_ID_WEB,
        redirectURI,
        codeChallenge: pkce.challenge,
        clientState: pkce.state,
        intent: input.intent === "register" ? "register" : "login",
        next: input.next,
      },
      input.intent === "bind",
    );
    if (!url) throw new Error("errors.generic");
    if (!desktop) {
      // A full document navigation: the provider must never be reached by XHR.
      window.location.assign(url);
      return;
    }
    const waiting = waitForLoopback(input.signal);
    if (!(await openExternal(url))) throw new Error("errors.generic");
    const callbackUrl = await waiting;
    const path = callbackPathFromLoopback(callbackUrl);
    if (!path) throw new Error("errors.generic");
    input.navigate(path);
  } catch (error) {
    dropBridgeRequest(input.slug);
    if (loopbackStarted) await invoke("stop_oauth_loopback").catch(() => undefined);
    throw error;
  }
}

/** A user-cancelled desktop wait is not an error worth a toast. */
export function isSilentProviderError(error: unknown): boolean {
  return isAbortError(error);
}
