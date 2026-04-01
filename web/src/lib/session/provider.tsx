"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useState, useSyncExternalStore, type ReactNode } from "react";
import { fetchMe, logoutRequest } from "@/lib/api/endpoints/auth";
import { isAbortError } from "@/lib/api/errors";
import { EMPTY_SESSION, endSession, sessionStore } from "@/lib/api/session-store";
import type { AuthUser } from "@/lib/api/types";
import { useI18n } from "@/lib/i18n/provider";

export const USER_UPDATED_EVENT = "riochat:user-profile-updated";

/** Lets any screen push a fresh profile into the session. */
export function publishUserUpdate(user: AuthUser): void {
  window.dispatchEvent(new CustomEvent(USER_UPDATED_EVENT, { detail: user }));
}

export type SessionStatus = "loading" | "ready" | "failed";

interface SessionValue {
  accessToken: string;
  user: AuthUser | null;
  status: SessionStatus;
  reloadUser: () => Promise<void>;
  signOut: () => Promise<void>;
}

const Ctx = createContext<SessionValue | null>(null);

const subscribe = (cb: () => void) => sessionStore.subscribe(cb);
const getSnapshot = () => sessionStore.get();
const getServerSnapshot = () => EMPTY_SESSION;

export function SessionProvider({ children }: { children: ReactNode }) {
  const snapshot = useSyncExternalStore(subscribe, getSnapshot, getServerSnapshot);
  const accessToken = snapshot.accessToken;
  const [user, setUser] = useState<AuthUser | null>(null);
  const [status, setStatus] = useState<SessionStatus>("loading");
  const [reloadCount, setReloadCount] = useState(0);

  // The profile reloads whenever the access token changes (or on demand).
  useEffect(() => {
    if (!accessToken) {
      setUser(null);
      setStatus("loading");
      return;
    }
    const controller = new AbortController();
    fetchMe(controller.signal).then(
      (next) => {
        setUser(next);
        setStatus("ready");
      },
      (error: unknown) => {
        if (!isAbortError(error)) setStatus("failed");
      },
    );
    return () => controller.abort();
  }, [accessToken, reloadCount]);

  useEffect(() => {
    const onUpdate = (event: Event) => {
      const detail = (event as CustomEvent<AuthUser>).detail;
      if (detail && typeof detail === "object") {
        setUser(detail);
        setStatus("ready");
      }
    };
    window.addEventListener(USER_UPDATED_EVENT, onUpdate);
    return () => window.removeEventListener(USER_UPDATED_EVENT, onUpdate);
  }, []);

  const reloadUser = useCallback(async () => setReloadCount((n) => n + 1), []);
  const signOut = useCallback(async () => {
    await logoutRequest();
    await endSession();
  }, []);

  const value = useMemo(
    () => ({ accessToken, user, status, reloadUser, signOut }),
    [accessToken, user, status, reloadUser, signOut],
  );

  return (
    <Ctx.Provider value={value}>
      <LocaleFollowsAccount user={user} />
      {children}
    </Ctx.Provider>
  );
}

/** Once the account is loaded (and past first-run security), its locale wins. */
function LocaleFollowsAccount({ user }: { user: AuthUser | null }) {
  const { setLocale } = useI18n();
  const locale = user?.locale;
  const gated = user?.initialSecurityRequired;
  useEffect(() => {
    if (locale && gated === false) void setLocale(locale);
  }, [locale, gated, setLocale]);
  return null;
}

export function useSession(): SessionValue {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useSession must be used inside <SessionProvider>");
  return ctx;
}

export function isAdminRole(role: string | undefined): boolean {
  return role === "admin" || role === "superadmin";
}
