import { absolutizeApiPath } from "../api/base-url";
import { manifestUrl } from "../api/endpoints/public";
import type { Branding } from "../api/types";

function upsertMeta(name: string, content: string): void {
  let el = document.head.querySelector<HTMLMetaElement>(`meta[name="${name}"]`);
  if (!el) {
    el = document.createElement("meta");
    el.name = name;
    document.head.appendChild(el);
  }
  el.content = content;
}

function upsertLink(selector: string, attrs: Record<string, string>): void {
  let el = document.head.querySelector<HTMLLinkElement>(selector);
  if (!el) {
    el = document.createElement("link");
    document.head.appendChild(el);
  }
  for (const [k, v] of Object.entries(attrs)) el.setAttribute(k, v);
}

function iconMimeType(url: string): string | null {
  const path = url.split(/[?#]/)[0]?.toLowerCase() ?? "";
  if (path.endsWith(".svg")) return "image/svg+xml";
  if (path.endsWith(".png")) return "image/png";
  if (path.endsWith(".ico")) return "image/x-icon";
  if (path.endsWith(".jpg") || path.endsWith(".jpeg")) return "image/jpeg";
  return null;
}

/**
 * Writes the active branding into the document head: title, description,
 * icons and the manifest link. Elements declared statically by the root
 * layout are updated in place so no duplicates accumulate.
 */
export function applyBrandingToHead(branding: Branding): void {
  if (typeof document === "undefined") return;
  document.title = branding.title;
  upsertMeta("application-name", branding.shortName || branding.title);
  upsertMeta("apple-mobile-web-app-title", branding.shortName || branding.title);
  upsertMeta("description", branding.description);

  const icon = (url: string) => absolutizeApiPath(url);
  upsertLink('link[rel="icon"]:not([sizes])', { rel: "icon", href: icon(branding.faviconURL) });
  // The static link may declare another type (the bundled SVG); drop or correct it.
  const favicon = document.head.querySelector<HTMLLinkElement>('link[rel="icon"]:not([sizes])');
  const type = iconMimeType(branding.faviconURL);
  if (type) favicon?.setAttribute("type", type);
  else favicon?.removeAttribute("type");
  upsertLink('link[rel="icon"][sizes="192x192"]', { rel: "icon", sizes: "192x192", type: "image/png", href: icon(branding.pwaIcon192URL) });
  upsertLink('link[rel="icon"][sizes="512x512"]', { rel: "icon", sizes: "512x512", type: "image/png", href: icon(branding.pwaIcon512URL) });
  upsertLink('link[rel="apple-touch-icon"]', { rel: "apple-touch-icon", sizes: "180x180", href: icon(branding.appleTouchIcon180URL) });
  upsertLink('link[rel="manifest"]', { rel: "manifest", href: manifestUrl() });
}
