"use client";

import { type MouseEvent, type ReactNode } from "react";
import { absolutizeApiPath } from "@/lib/api/base-url";
import { askToOpen, classifyLink } from "../../state/link-confirm";
import { useMarkdownContext } from "./context";

/** Smooth-scrolls to an anchor inside the message scope; ids are tried as given, decoded and prefixed. */
export function scrollToAnchor(id: string, scope: string): boolean {
  const raw = id.replace(/^#/, "");
  let decoded = raw;
  try {
    decoded = decodeURIComponent(raw);
  } catch {
    /* keep raw */
  }
  const candidates = [raw, decoded, `${scope}-${raw}`, `${scope}-${decoded}`, `user-content-${decoded}`];
  for (const c of candidates) {
    const el = document.getElementById(c);
    if (el) {
      el.scrollIntoView({ behavior: window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth", block: "start" });
      if (el.tabIndex < 0) el.tabIndex = -1;
      el.focus({ preventScroll: true });
      return true;
    }
  }
  const suffix = Array.from(document.querySelectorAll<HTMLElement>(`[id$="-${CSS.escape(decoded)}"]`)).find((el) => el.id.startsWith(scope));
  if (suffix) {
    suffix.scrollIntoView({ behavior: "smooth", block: "start" });
    return true;
  }
  return false;
}

export function MdLink({ href, title, children }: { href: string; title: string; children: ReactNode }) {
  const ctx = useMarkdownContext();
  const kind = classifyLink(href, typeof window === "undefined" ? "" : window.location.origin);
  if (kind === "invalid") return <>{children}</>;
  if (href.startsWith("#")) {
    return (
      <a
        href={href}
        title={title || undefined}
        onClick={(e: MouseEvent) => {
          e.preventDefault();
          scrollToAnchor(href, ctx.scope);
        }}
      >
        {children}
      </a>
    );
  }
  if (kind === "external") {
    return (
      <a
        href={href}
        title={title || undefined}
        target="_blank"
        rel="noreferrer noopener"
        onClick={(e: MouseEvent) => {
          if (e.metaKey || e.ctrlKey || e.shiftKey || e.button !== 0) return;
          e.preventDefault();
          askToOpen(href);
        }}
      >
        {children}
      </a>
    );
  }
  return (
    <a href={absolutizeApiPath(href)} title={title || undefined} {...(kind === "special" ? { target: "_blank", rel: "noreferrer noopener" } : {})}>
      {children}
    </a>
  );
}
