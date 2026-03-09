"use client";

import { useSyncExternalStore } from "react";

/** Subscribes to a media query; `false` during static rendering. */
export function useMediaQuery(query: string): boolean {
  return useSyncExternalStore(
    (notify) => {
      const mq = window.matchMedia(query);
      mq.addEventListener("change", notify);
      return () => mq.removeEventListener("change", notify);
    },
    () => window.matchMedia(query).matches,
    () => false,
  );
}

/** Below the `md` breakpoint (768px). */
export function useIsMobile(): boolean {
  return useMediaQuery("(max-width: 767px)");
}
