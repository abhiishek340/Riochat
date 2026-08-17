"use client";

import { useEffect, useState } from "react";
import { useSession } from "@/lib/session/provider";
import { fetchAccess, type ProviderAccess } from "./api";

export interface AccessState {
  /** Null until the answer is in; a failed request counts as "not enabled". */
  access: ProviderAccess | null;
  loaded: boolean;
}

const DISABLED: ProviderAccess = { enabled: false, maxPerUser: 0, protocols: [], modelProtocols: [] };

// One request per signed-in user, shared by the navigation and the pages.
let cached: { who: string; promise: Promise<ProviderAccess> } | null = null;

function load(who: string, force = false): Promise<ProviderAccess> {
  if (!force && cached?.who === who) return cached.promise;
  const promise = fetchAccess().catch(() => DISABLED);
  cached = { who, promise };
  return promise;
}

/** Raised by the administration console after it changes the personal-provider policy. */
export const PROVIDER_ACCESS_CHANGED_EVENT = "riochat:personal-provider-access-changed";

/** Forgets the shared answer so the next reader (and every mounted reader) asks again. */
export function invalidateProviderAccess(): void {
  cached = null;
}

// A change made while no reader is mounted must not leave a stale answer behind.
if (typeof window !== "undefined") window.addEventListener(PROVIDER_ACCESS_CHANGED_EVENT, invalidateProviderAccess);

/** The personal-provider policy for the signed-in user (fail closed). */
export function useProviderAccess(): AccessState {
  const { user, accessToken } = useSession();
  const who = accessToken && user ? user.publicID : "";
  const [state, setState] = useState<{ who: string; access: ProviderAccess } | null>(null);

  useEffect(() => {
    if (!who) return;
    let live = true;
    void load(who).then((access) => {
      if (live) setState({ who, access });
    });
    return () => {
      live = false;
    };
  }, [who]);

  // A policy change made in the console refreshes whoever is showing the flag.
  useEffect(() => {
    if (!who) return;
    let live = true;
    const refresh = () => {
      invalidateProviderAccess();
      void load(who, true).then((access) => {
        if (live) setState({ who, access });
      });
    };
    window.addEventListener(PROVIDER_ACCESS_CHANGED_EVENT, refresh);
    return () => {
      live = false;
      window.removeEventListener(PROVIDER_ACCESS_CHANGED_EVENT, refresh);
    };
  }, [who]);

  const ready = state !== null && state.who === who && who !== "";
  return { access: ready ? state.access : null, loaded: ready };
}
