"use client";

import { useEffect, useState, type ReactNode } from "react";
import { setApiBaseOverride } from "@/lib/api/base-url";
import { registerNativeRefresh } from "@/lib/api/endpoints/refresh-transport";
import { isDesktop } from "@/lib/api/platform";
import { sessionStore, setSignOutCleanup } from "@/lib/api/session-store";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { shell } from "./bridge";
import { ServerSetup } from "./setup-page";

type Phase = "pending" | "ready" | "setup";

let boundMode: "local" | "remote" | null = null;

/**
 * Points the web app at the shell's server: API base, native refresh, sign-out
 * cleanup and (for the local server) a session. Safe to call more than once.
 */
export async function bindShellServer(server: { mode: "local" | "remote"; origin: string }): Promise<void> {
  boundMode = server.mode;
  setApiBaseOverride(() => server.origin);
  registerNativeRefresh(() => shell.refreshSession());
  setSignOutCleanup(async () => {
    // The local server has no login page: signing out means leaving it.
    if (boundMode === "local") await shell.leaveServer();
    else await shell.clearSession();
  });
  if (server.mode === "local") {
    try {
      const credentials = await shell.localSignIn();
      if (credentials) sessionStore.set(credentials);
    } catch {
      /* the login page offers a retry */
    }
  }
}

/**
 * In a browser this renders its children at once. In the desktop shell it first
 * asks which server this install uses: none -> the setup screen; local -> make
 * sure a local session exists. Failing to start the bundled server sends the
 * user back to the setup screen with the reason.
 */
export function DesktopGate({ children }: { children: ReactNode }) {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [desktop, setDesktop] = useState<boolean | null>(null);
  const [phase, setPhase] = useState<Phase>("pending");

  useEffect(() => {
    const isShell = isDesktop();
    setDesktop(isShell);
    if (!isShell) return;
    let live = true;
    void (async () => {
      try {
        const server = await shell.getServer();
        if (!live) return;
        if (!server) {
          setPhase("setup");
          return;
        }
        await bindShellServer(server);
        if (live) setPhase("ready");
      } catch (error) {
        if (!live) return;
        toast.error(t("desktopSetup.errors.bootstrapFailed"), { description: errorMessage(error) });
        await shell.leaveServer().catch(() => undefined);
        setPhase("setup");
      }
    })();
    return () => {
      live = false;
    };
    // The bootstrap runs once per page load.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  if (desktop === null) return null;
  if (!desktop) return <>{children}</>;
  if (phase === "pending") return null;
  if (phase === "setup") {
    return (
      <ServerSetup
        onBound={async () => {
          const server = await shell.getServer();
          if (server) await bindShellServer(server);
          setPhase("ready");
        }}
      />
    );
  }
  return <>{children}</>;
}
