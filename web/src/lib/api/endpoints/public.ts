import { resolveApiBase } from "../base-url";
import { publicApi } from "../client";
import type { Branding, VersionInfo } from "../types";
import { isRecord, optStr, str } from "../../utils/guards";

export const DEFAULT_BRANDING: Branding = {
  title: "Riochat",
  shortName: "Riochat",
  description: "Riochat is a multi-model AI chat studio.",
  logoURL: "",
  faviconURL: "/icon.svg",
  pwaIcon192URL: "/pwa/icon-192.png",
  pwaIcon512URL: "/pwa/icon-512.png",
  pwaMaskableIcon512URL: "/pwa/icon-maskable-512.png",
  appleTouchIcon180URL: "/pwa/apple-touch-icon.png",
};

export async function fetchBranding(): Promise<Branding> {
  const data = await publicApi.get<unknown>("/branding");
  const d = isRecord(data) ? data : {};
  const pick = (key: keyof Branding) => str(d[key]).trim() || DEFAULT_BRANDING[key];
  return {
    title: pick("title"),
    shortName: pick("shortName"),
    description: pick("description"),
    logoURL: str(d.logoURL).trim(),
    faviconURL: pick("faviconURL"),
    pwaIcon192URL: pick("pwaIcon192URL"),
    pwaIcon512URL: pick("pwaIcon512URL"),
    pwaMaskableIcon512URL: pick("pwaMaskableIcon512URL"),
    appleTouchIcon180URL: pick("appleTouchIcon180URL"),
  };
}

export function manifestUrl(): string {
  return `${resolveApiBase()}/api/v1/branding/manifest.webmanifest`;
}

export async function fetchCapabilitiesPayload(): Promise<unknown> {
  return publicApi.get<unknown>("/capabilities");
}

export async function fetchLoginPage(): Promise<{ defaultNextPath: string }> {
  const data = await publicApi.get<unknown>("/settings/login-page");
  return { defaultNextPath: isRecord(data) ? str(data.defaultNextPath) : "" };
}

/** `data` may be bare or enveloped depending on server age; plain fetch on purpose. */
export async function fetchVersion(signal?: AbortSignal): Promise<VersionInfo> {
  const response = await fetch(`${resolveApiBase()}/api/v1/version`, { cache: "no-store", credentials: "include", signal });
  if (!response.ok) throw new Error(`version request failed (${response.status})`);
  const body: unknown = await response.json();
  const inner = isRecord(body) && isRecord(body.data) ? body.data : body;
  const d = isRecord(inner) ? inner : {};
  return {
    product: optStr(d.product),
    version: optStr(d.version),
    commit: optStr(d.commit),
    buildTime: optStr(d.buildTime),
    buildID: optStr(d.buildID),
  };
}

/** Comparable build identity: buildID, else the informative parts joined. */
export function buildIdOf(info: VersionInfo): string {
  const id = info.buildID?.trim();
  if (id) return id;
  return [info.version, info.commit, info.buildTime]
    .map((v) => v?.trim() ?? "")
    .filter((v) => v && v !== "unknown" && v !== "dev")
    .join("-");
}
