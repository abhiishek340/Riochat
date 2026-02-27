/** PKCE (RFC 7636, S256) helpers and a timing-safe string comparison. */

function toBase64Url(bytes: Uint8Array): string {
  let binary = "";
  for (const b of bytes) binary += String.fromCharCode(b);
  return btoa(binary).replace(/\+/g, "-").replace(/\//g, "_").replace(/=+$/, "");
}

/** `byteCount` random bytes as unpadded base64url (48 bytes -> 64 chars). */
export function randomToken(byteCount: number): string {
  const bytes = new Uint8Array(byteCount);
  crypto.getRandomValues(bytes);
  return toBase64Url(bytes);
}

export async function challengeFor(verifier: string): Promise<string> {
  const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(verifier));
  return toBase64Url(new Uint8Array(digest));
}

export interface PkcePair {
  verifier: string;
  challenge: string;
  state: string;
}

export async function createPkcePair(): Promise<PkcePair> {
  const verifier = randomToken(48);
  return { verifier, challenge: await challengeFor(verifier), state: randomToken(32) };
}

/** Compares without exiting at the first differing character. */
export function constantTimeEqual(a: string, b: string): boolean {
  const left = new TextEncoder().encode(a);
  const right = new TextEncoder().encode(b);
  let diff = left.length ^ right.length;
  const length = Math.max(left.length, right.length);
  for (let i = 0; i < length; i++) diff |= (left[i] ?? 0) ^ (right[i] ?? 0);
  return diff === 0;
}
