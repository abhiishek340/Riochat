"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { resolveApiBase, subscribeApiBase } from "@/lib/api/base-url";
import { DEFAULT_BRANDING, fetchBranding } from "@/lib/api/endpoints/public";
import type { Branding } from "@/lib/api/types";
import { applyBrandingToHead } from "./head";

interface BrandingState {
  branding: Branding;
  /** True once branding arrived, the request failed, or 3 s passed. */
  ready: boolean;
}

const Ctx = createContext<BrandingState>({ branding: DEFAULT_BRANDING, ready: false });

const READY_DEADLINE_MS = 3000;
const RETRY_DELAY_MS = 1000;

// One cached promise per server; failures are dropped so a later attempt retries.
const cache = new Map<string, Promise<Branding>>();

function loadBranding(base: string): Promise<Branding> {
  let pending = cache.get(base);
  if (!pending) {
    pending = fetchBranding();
    cache.set(base, pending);
    pending.catch(() => cache.delete(base));
  }
  return pending;
}

/** Non-React code (notifications) reads the latest branding from here. */
let latest: Branding = DEFAULT_BRANDING;
export function currentBranding(): Branding {
  return latest;
}

export function BrandingProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<BrandingState>({ branding: DEFAULT_BRANDING, ready: false });
  const [generation, setGeneration] = useState(0);
  const generationRef = useRef(0);

  const bump = useCallback(() => {
    generationRef.current += 1;
    setGeneration(generationRef.current);
  }, []);
  useEffect(() => subscribeApiBase(bump), [bump]);

  useEffect(() => {
    // A response that belongs to a previously selected server must never
    // replace the current server's branding.
    const mine = generationRef.current;
    const base = resolveApiBase();
    if (mine > 0) setState({ branding: DEFAULT_BRANDING, ready: false });
    let disposed = false;
    const stale = () => disposed || mine !== generationRef.current || base !== resolveApiBase();

    const accept = (branding: Branding) => {
      if (stale()) return;
      latest = branding;
      applyBrandingToHead(branding);
      setState({ branding, ready: true });
    };
    const markReady = () => {
      if (!stale()) setState((s) => (s.ready ? s : { ...s, ready: true }));
    };

    const deadline = window.setTimeout(markReady, READY_DEADLINE_MS);
    let retry: number | undefined;
    loadBranding(base).then(accept, () => {
      markReady();
      retry = window.setTimeout(() => {
        if (stale()) return;
        loadBranding(base).then(accept, () => undefined);
      }, RETRY_DELAY_MS);
    });

    return () => {
      disposed = true;
      window.clearTimeout(deadline);
      if (retry !== undefined) window.clearTimeout(retry);
    };
  }, [generation]);

  const value = useMemo(() => state, [state]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useBranding(): BrandingState {
  return useContext(Ctx);
}
