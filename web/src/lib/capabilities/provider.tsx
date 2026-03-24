"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { subscribeApiBase, resolveApiBase } from "@/lib/api/base-url";
import { fetchCapabilitiesPayload } from "@/lib/api/endpoints/public";
import { onFeatureDisabled } from "@/lib/api/http";
import { ALL_ENABLED, readCapabilities, type Capabilities, type FeatureName } from "./features";

interface CapabilitiesState {
  flags: Capabilities;
  /** False until an answer (or the fail-open fallback) applied for the current server. */
  loaded: boolean;
}

const Ctx = createContext<CapabilitiesState & { reload: () => void }>({
  flags: ALL_ENABLED,
  loaded: false,
  reload: () => undefined,
});

// One shared request per server origin.
const cache = new Map<string, Promise<Capabilities>>();

function load(base: string): Promise<Capabilities> {
  let pending = cache.get(base);
  if (!pending) {
    pending = fetchCapabilitiesPayload().then(readCapabilities, () => ALL_ENABLED);
    cache.set(base, pending);
  }
  return pending;
}

export function CapabilitiesProvider({ children }: { children: ReactNode }) {
  const [state, setState] = useState<CapabilitiesState>({ flags: ALL_ENABLED, loaded: false });
  const [generation, setGeneration] = useState(0);

  const reload = useCallback(() => {
    cache.delete(resolveApiBase());
    setGeneration((g) => g + 1);
  }, []);

  useEffect(() => {
    onFeatureDisabled(reload);
    const off = subscribeApiBase(reload);
    return () => {
      onFeatureDisabled(null);
      off();
    };
  }, [reload]);

  useEffect(() => {
    let live = true;
    const base = resolveApiBase();
    setState((s) => (s.loaded ? { ...s, loaded: false } : s));
    void load(base).then((flags) => {
      if (live && base === resolveApiBase()) setState({ flags, loaded: true });
    });
    return () => {
      live = false;
    };
  }, [generation]);

  const value = useMemo(() => ({ ...state, reload }), [state, reload]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useCapabilities() {
  return useContext(Ctx);
}

/** Children render only while the flag is on (before loading, flags are on). */
export function FeatureGate({ feature, children, fallback = null }: { feature: FeatureName; children: ReactNode; fallback?: ReactNode }) {
  const { flags } = useCapabilities();
  return flags[feature] ? <>{children}</> : <>{fallback}</>;
}

export interface FeatureScoped {
  feature?: FeatureName;
}

export function isAvailable(entry: FeatureScoped, flags: Capabilities): boolean {
  return !entry.feature || flags[entry.feature];
}
