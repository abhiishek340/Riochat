/**
 * Post-login destinations must be same-origin absolute paths. `//host` and
 * `https://host` would turn the login page into an open redirect.
 */
export function safeNextPath(value: string | null | undefined, fallback = "/chat"): string {
  if (typeof value !== "string") return fallback;
  if (!value.startsWith("/") || value.startsWith("//") || value.includes("\\")) return fallback;
  // Browsers drop tabs and line breaks while parsing a URL, so "/\t/host" would turn into "//host".
  if (/[\u0000-\u001f\u007f]/.test(value)) return fallback;
  return value;
}

/** Path plus query, never the fragment (it may carry a secret). */
export function currentPathAndQuery(loc: Pick<Location, "pathname" | "search">): string {
  return `${loc.pathname}${loc.search}`;
}

export function loginUrlFor(path: string): string {
  return `/login?next=${encodeURIComponent(path)}`;
}
