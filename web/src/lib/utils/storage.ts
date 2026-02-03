/**
 * Browser storage that never throws. Private windows, blocked site data and
 * server-side rendering all make the accessors fail or vanish, so every read
 * falls back to a default and every write is swallowed.
 */

type Area = "local" | "session";

function area(kind: Area): Storage | null {
  try {
    if (typeof window === "undefined") return null;
    return kind === "local" ? window.localStorage : window.sessionStorage;
  } catch {
    return null;
  }
}

export function readStorage(key: string, kind: Area = "local"): string | null {
  try {
    return area(kind)?.getItem(key) ?? null;
  } catch {
    return null;
  }
}

export function writeStorage(key: string, value: string, kind: Area = "local"): void {
  try {
    area(kind)?.setItem(key, value);
  } catch {
    /* storage unavailable or full: the preference simply does not persist */
  }
}

export function removeStorage(key: string, kind: Area = "local"): void {
  try {
    area(kind)?.removeItem(key);
  } catch {
    /* ignore */
  }
}

/** Reads "true"/"false"; anything else yields the fallback. */
export function readBoolean(key: string, fallback: boolean): boolean {
  const raw = readStorage(key);
  return raw === "true" ? true : raw === "false" ? false : fallback;
}

export function writeBoolean(key: string, value: boolean): void {
  writeStorage(key, value ? "true" : "false");
}

export function readCookie(name: string): string | null {
  try {
    if (typeof document === "undefined") return null;
    for (const part of document.cookie.split(";")) {
      const idx = part.indexOf("=");
      if (idx < 0) continue;
      if (part.slice(0, idx).trim() === name) return decodeURIComponent(part.slice(idx + 1).trim());
    }
  } catch {
    /* ignore */
  }
  return null;
}

export function writeCookie(name: string, value: string, maxAgeSeconds: number): void {
  try {
    if (typeof document === "undefined") return;
    document.cookie = `${name}=${encodeURIComponent(value)}; Path=/; Max-Age=${maxAgeSeconds}; SameSite=Lax`;
  } catch {
    /* ignore */
  }
}
