"use client";

import { useEffect, useState, type ReactNode } from "react";
import { usePathname, useRouter } from "next/navigation";
import { resolveAccessToken } from "@/lib/api/client";
import { SESSION_CHANGED_EVENT, sessionStore } from "@/lib/api/session-store";
import { useT } from "@/lib/i18n/provider";
import { loginUrlFor } from "@/lib/utils/navigation";
import { LoadingScreen } from "@/components/shell/loading-screen";

/**
 * Protects authenticated routes. With a cached token it is immediately ready;
 * otherwise it is "checking" while a token is obtained (cache, then refresh).
 * With no obtainable token it replaces the history entry with
 * `/login?next=<path+query>` (never the fragment: it may hold a secret).
 */
export function AuthGuard({ children }: { children: ReactNode }) {
  const router = useRouter();
  const pathname = usePathname();
  const t = useT();
  const [ready, setReady] = useState(() => sessionStore.token() !== "");

  useEffect(() => {
    let live = true;
    const goToLogin = () => {
      const search = typeof window === "undefined" ? "" : window.location.search;
      router.replace(loginUrlFor(`${pathname}${search}`));
    };

    if (sessionStore.token()) {
      setReady(true);
    } else {
      setReady(false);
      void resolveAccessToken().then((token) => {
        if (!live) return;
        if (token) setReady(true);
        else goToLogin();
      });
    }

    const onChange = () => {
      if (sessionStore.token()) {
        setReady(true);
      } else {
        setReady(false);
        goToLogin();
      }
    };
    window.addEventListener(SESSION_CHANGED_EVENT, onChange);
    return () => {
      live = false;
      window.removeEventListener(SESSION_CHANGED_EVENT, onChange);
    };
  }, [pathname, router]);

  if (!ready) return <LoadingScreen label={t("common.states.loading")} />;
  return <>{children}</>;
}
