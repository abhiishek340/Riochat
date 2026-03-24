/**
 * Server addresses the desktop shell may bind to. Stricter than the shared
 * `normalizeServerUrl`: only an origin, never a path, query, fragment or
 * credentials, because the shell keys credentials by this exact string.
 */
export function normalizeOrigin(input: string | null | undefined): string {
  const value = (input ?? "").trim();
  if (!value || /[?#\s]/.test(value)) return "";
  let url: URL;
  try {
    url = new URL(value);
  } catch {
    return "";
  }
  if (url.protocol !== "http:" && url.protocol !== "https:") return "";
  if (!url.hostname || url.username || url.password) return "";
  if (url.pathname !== "/" && url.pathname !== "") return "";
  return `${url.protocol}//${url.host}`.toLowerCase();
}

export const PROBE_TIMEOUT_MS = 10_000;

export type ProbeResult = { kind: "ok" } | { kind: "timeout" } | { kind: "network" } | { kind: "status"; status: number };

/**
 * Asks `<origin>/healthz` whether a Riochat server answers there. An address
 * that fails the probe must not be remembered across restarts, so callers pin
 * the server only after `ok`.
 */
export async function probeServer(
  origin: string,
  options: { fetchImpl?: typeof fetch; timeoutMs?: number } = {},
): Promise<ProbeResult> {
  const doFetch = options.fetchImpl ?? fetch;
  const timeout = options.timeoutMs ?? PROBE_TIMEOUT_MS;
  const controller = new AbortController();
  let timedOut = false;
  const timer = setTimeout(() => {
    timedOut = true;
    controller.abort();
  }, timeout);
  try {
    const response = await doFetch(`${origin}/healthz`, { cache: "no-store", signal: controller.signal, redirect: "manual" });
    // A cross-origin opaque redirect is not a healthy answer either.
    if (response.type === "opaqueredirect") return { kind: "status", status: 302 };
    return response.ok ? { kind: "ok" } : { kind: "status", status: response.status };
  } catch {
    return timedOut ? { kind: "timeout" } : { kind: "network" };
  } finally {
    clearTimeout(timer);
  }
}
