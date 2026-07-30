/**
 * Starts linking another sign-in identity to the signed-in account. It uses
 * the same PKCE bridge as signing in; the callback page finishes the job and
 * returns to `next`.
 */
import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { isRecord, str } from "@/lib/utils/guards";
import { writeStorage } from "@/lib/utils/storage";

export const CLIENT_ID_WEB = "riochat-web";
/** Server transaction window + grant window + clock skew. */
export const BRIDGE_LIFETIME_MS = (10 * 60 + 90 + 30) * 1000;

export function bridgeKey(slug: string): string {
  return `riochat:oauth:${slug}:bridge`;
}

function base64url(bytes: Uint8Array): string {
  let text = "";
  for (const b of bytes) text += String.fromCharCode(b);
  return btoa(text).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

function randomToken(byteLength: number): string {
  const bytes = new Uint8Array(byteLength);
  crypto.getRandomValues(bytes);
  return base64url(bytes);
}

/** S256 challenge of a verifier. */
export async function challengeFor(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
  return base64url(new Uint8Array(digest));
}

export async function startBind(slug: string, next: string): Promise<void> {
  const verifier = randomToken(48);
  const state = randomToken(32);
  const challenge = await challengeFor(verifier);
  writeStorage(
    bridgeKey(slug),
    JSON.stringify({ verifier, state, intent: "bind", next, expiresAt: Date.now() + BRIDGE_LIFETIME_MS }),
    "session",
  );
  try {
    const data = await api.post<unknown>(`/me/identities/providers/${seg(slug)}/authorize`, {
      clientID: CLIENT_ID_WEB,
      redirectURI: `${window.location.origin}/auth/callback?provider=${encodeURIComponent(slug)}`,
      codeChallenge: challenge,
      clientState: state,
      next,
    });
    const url = isRecord(data) ? str(data.authorizationURL) : "";
    if (!url) throw new Error("errors.generic");
    // A full navigation: the provider is never reached by XHR.
    window.location.assign(url);
  } catch (error) {
    try {
      window.sessionStorage.removeItem(bridgeKey(slug));
    } catch {
      /* storage unavailable */
    }
    throw error;
  }
}
