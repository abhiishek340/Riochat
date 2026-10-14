/** Release check: version comparison and the cached "latest release" record. */
import { isRecord } from "@/lib/utils/guards";
import { readStorage, writeStorage } from "@/lib/utils/storage";

export const LATEST_RELEASE_URL = "https://api.github.com/repos/riochat/riochat/releases/latest";
export const LATEST_RELEASE_KEY = "riochat:admin:latest-release";
export const LATEST_RELEASE_EVENT = "riochat:latest-release-changed";
export const CHECK_TIMEOUT_MS = 10_000;

export interface LatestRelease {
  version: string;
  url: string;
}

/** Strips a leading `v` and returns `[major, minor, patch]` from the first `n(.n){0,2}` run. */
export function parseVersion(text: string | null | undefined): [number, number, number] | null {
  const match = /(\d+)(?:\.(\d+))?(?:\.(\d+))?/.exec((text ?? "").trim().replace(/^v/i, ""));
  if (!match) return null;
  return [Number(match[1]), Number(match[2] ?? 0), Number(match[3] ?? 0)];
}

/** -1, 0 or 1; null when either side cannot be read. */
export function compareVersions(a: string | null | undefined, b: string | null | undefined): -1 | 0 | 1 | null {
  const left = parseVersion(a);
  const right = parseVersion(b);
  if (!left || !right) return null;
  for (let i = 0; i < 3; i++) {
    const x = left[i] as number;
    const y = right[i] as number;
    if (x !== y) return x < y ? -1 : 1;
  }
  return 0;
}

export function withV(version: string): string {
  const v = version.trim();
  return /^v/i.test(v) ? v : `v${v}`;
}

export function releasePageUrl(version: string): string {
  return `https://github.com/riochat/riochat/releases/tag/${encodeURIComponent(withV(version))}`;
}

/** The latest release is shown as an update when it is newer or the comparison is unknown. */
export function isUpdateAvailable(current: string | null | undefined, latest: string): boolean {
  const order = compareVersions(current, latest);
  return order === null || order < 0;
}

/** Accepts only `{ tag_name: string, html_url: string }` with non-blank values. */
export function readRelease(payload: unknown): LatestRelease | null {
  if (!isRecord(payload)) return null;
  const tag = payload.tag_name;
  const url = payload.html_url;
  if (typeof tag !== "string" || typeof url !== "string") return null;
  if (!tag.trim() || !url.trim()) return null;
  return { version: tag.trim(), url: url.trim() };
}

export function readCachedRelease(): LatestRelease | null {
  const raw = readStorage(LATEST_RELEASE_KEY);
  if (!raw) return null;
  try {
    const value: unknown = JSON.parse(raw);
    return isRecord(value) ? readRelease({ tag_name: value.version, html_url: value.url }) : null;
  } catch {
    return null;
  }
}

export function cacheRelease(release: LatestRelease): void {
  writeStorage(LATEST_RELEASE_KEY, JSON.stringify(release));
  try {
    window.dispatchEvent(new CustomEvent(LATEST_RELEASE_EVENT));
  } catch {
    /* no window */
  }
}

export type CheckOutcome =
  | { state: "available"; latest: LatestRelease }
  | { state: "current"; latest: LatestRelease }
  | { state: "failed" };

/**
 * Asks GitHub for the latest release. The timeout covers the time until the
 * response headers arrive. Any failure (network, status, malformed body) is
 * reported as `failed`; a good answer is cached for the sidebar badge.
 */
export async function checkLatestRelease(current: string | null | undefined, fetcher: typeof fetch = fetch, timeoutMs = CHECK_TIMEOUT_MS): Promise<CheckOutcome> {
  const controller = new AbortController();
  const timer = setTimeout(() => controller.abort(), timeoutMs);
  let response: Response;
  try {
    response = await fetcher(LATEST_RELEASE_URL, { headers: { Accept: "application/vnd.github+json" }, cache: "no-store", signal: controller.signal });
  } catch {
    return { state: "failed" };
  } finally {
    clearTimeout(timer);
  }
  if (!response.ok) return { state: "failed" };
  let body: unknown;
  try {
    body = await response.json();
  } catch {
    return { state: "failed" };
  }
  const latest = readRelease(body);
  if (!latest) return { state: "failed" };
  cacheRelease(latest);
  return { state: isUpdateAvailable(current, latest.version) ? "available" : "current", latest };
}
