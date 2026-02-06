/**
 * Where the API lives.
 *
 * Resolution order: runtime override (desktop server picker) -> build-time
 * `NEXT_PUBLIC_API_BASE_URL` -> same origin (the empty string, so requests use
 * relative URLs). The dev proxy and the backend-hosted export both make the
 * page and the API share an origin, so no port guessing is done.
 */

const HOST_RE = /^(?:[a-z0-9](?:[a-z0-9-]*[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]*[a-z0-9])?)*|\d{1,3}(?:\.\d{1,3}){3}|\[[0-9a-f:.]+\])$/i;

/**
 * Validates and canonicalises a server address. Returns "" for anything that
 * is not an absolute http(s) URL of the form scheme://host[:port][/path];
 * credentials, queries and fragments can never survive.
 */
export function normalizeServerUrl(input: string | null | undefined): string {
  const value = (input ?? "").trim();
  const schemeEnd = value.indexOf("://");
  if (schemeEnd < 0) return "";
  const scheme = value.slice(0, schemeEnd).toLowerCase();
  if (scheme !== "http" && scheme !== "https") return "";
  const rest = value.slice(schemeEnd + 3);
  let hostPort = rest;
  let path = "";
  const slash = rest.indexOf("/");
  if (slash >= 0) {
    hostPort = rest.slice(0, slash);
    path = rest.slice(slash);
  }
  if (/[\s?#@]/.test(path) || /[\s?#@]/.test(hostPort)) return "";
  let host = hostPort;
  let port = "";
  const portMatch = /:(\d{1,5})$/.exec(hostPort);
  if (portMatch) {
    port = portMatch[1] ?? "";
    host = hostPort.slice(0, hostPort.length - port.length - 1);
    const n = Number(port);
    if (n < 1 || n > 65535) return "";
  }
  if (!host || !HOST_RE.test(host)) return "";
  // Trim trailing slashes without a backtracking regex.
  let end = path.length;
  while (end > 0 && path[end - 1] === "/") end--;
  path = path.slice(0, end);
  return `${scheme}://${host.toLowerCase()}${port ? `:${port}` : ""}${path}`;
}

type Resolver = () => string;
let override: Resolver | null = null;
const listeners = new Set<() => void>();

/** Desktop/mobile shells register the bound server here. */
export function setApiBaseOverride(resolver: Resolver | null): void {
  override = resolver;
  for (const l of listeners) l();
}

export function subscribeApiBase(listener: () => void): () => void {
  listeners.add(listener);
  return () => listeners.delete(listener);
}

export function resolveApiBase(): string {
  const fromOverride = normalizeServerUrl(override?.());
  if (fromOverride) return fromOverride;
  const configured = normalizeServerUrl(process.env.NEXT_PUBLIC_API_BASE_URL);
  if (configured) return configured;
  return "";
}

/** Absolute (or origin-relative) URL of an API path such as `/me`. */
export function apiUrl(path: string): string {
  return `${resolveApiBase()}/api/v1${path.startsWith("/") ? path : `/${path}`}`;
}

/**
 * Rewrites `/api/v1/...` references (model output, avatar paths) onto the API
 * base; every other reference is returned untouched.
 */
export function absolutizeApiPath(ref: string): string {
  if (ref.startsWith("/api/v1/")) return `${resolveApiBase()}${ref}`;
  return ref;
}
