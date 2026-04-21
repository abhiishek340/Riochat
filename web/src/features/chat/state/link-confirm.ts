"use client";

import { createStore } from "./store";

/** An external link waiting for the user's confirmation. */
export const linkConfirmStore = createStore<string | null>(null);

export function askToOpen(url: string): void {
  linkConfirmStore.set(url);
}

export type LinkKind = "invalid" | "internal" | "external" | "special";

/** Classifies a link target against the current origin. */
export function classifyLink(href: string, origin: string): LinkKind {
  const h = href.trim();
  if (!h || /^(javascript|data|vbscript):/i.test(h)) return "invalid";
  if (h.startsWith("#")) return "internal";
  if (h.startsWith("/") && !h.startsWith("//")) return "internal";
  let url: URL;
  try {
    url = new URL(h, origin || "http://localhost");
  } catch {
    return "invalid";
  }
  if (url.protocol === "http:" || url.protocol === "https:") return origin && url.origin === origin ? "internal" : "external";
  return "special";
}
