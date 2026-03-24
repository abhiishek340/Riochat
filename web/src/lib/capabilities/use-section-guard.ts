"use client";

import { useEffect } from "react";
import { useRouter } from "next/navigation";
import type { FeatureName } from "./features";
import { useCapabilities } from "./provider";

/**
 * Route-level check: once capabilities are loaded and the section's feature is
 * off, replace the history entry with `fallbackHref`. Returns "blocked" while
 * the caller should render nothing.
 */
export function useSectionGuard(feature: FeatureName | undefined, fallbackHref = "/chat"): "ok" | "blocked" {
  const { flags, loaded } = useCapabilities();
  const router = useRouter();
  const off = feature !== undefined && !flags[feature];
  useEffect(() => {
    if (loaded && off) router.replace(fallbackHref);
  }, [loaded, off, router, fallbackHref]);
  return loaded && off ? "blocked" : "ok";
}
