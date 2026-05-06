"use client";

import dynamic from "next/dynamic";
import { usePathname } from "next/navigation";
import { useEffect, type ReactNode } from "react";
import { AppVersionGuard } from "@/components/shell/app-version-guard";
import { DesktopGate, isStripRoute } from "@/features/auth-shell-share";
import { Toaster } from "@/components/ui/toaster";
import { TooltipProvider } from "@/components/ui/tooltip";
import { BrandingProvider, useBranding } from "@/lib/branding/provider";
import { CapabilitiesProvider } from "@/lib/capabilities/provider";
import { I18nProvider, useI18n } from "@/lib/i18n/provider";
import { SessionProvider } from "@/lib/session/provider";
import { ThemeProvider } from "@/lib/theme/provider";

// Font preferences and the account-stored appearance; loaded after first paint.
const AppearanceRuntime = dynamic(() => import("@/features/settings").then((m) => m.AppearanceRuntime), { ssr: false });

/**
 * Provider order matters:
 *   branding  -> i18n (brand name is injected into every message)
 *   i18n/theme -> everything that renders text or colour
 *   session   -> user + token; capabilities load independently of sign-in
 * The toast host sits outside any guard so toasts also work on the login page.
 */
export function RootProviders({ children }: { children: ReactNode }) {
  const pathname = usePathname();
  // The desktop tab strip is a tiny webview of its own: no branding fetch, no
  // message bundles, no guards. It styles itself from the theme tokens alone.
  if (isStripRoute(pathname)) return <>{children}</>;
  return (
    <BrandingProvider>
      <I18nProvider>
        <ThemeProvider>
          <TooltipProvider>
            <SessionProvider>
              <CapabilitiesProvider>
                <DesktopGate>{children}</DesktopGate>
                <AppearanceRuntime />
                <AppVersionGuard />
              </CapabilitiesProvider>
            </SessionProvider>
            <Toaster />
            <RevealGate />
          </TooltipProvider>
        </ThemeProvider>
      </I18nProvider>
    </BrandingProvider>
  );
}

/** Lifts the "branding pending" veil once both branding and the catalog are in place. */
function RevealGate() {
  const { ready: brandingReady } = useBranding();
  const { ready: i18nReady } = useI18n();
  const ready = brandingReady && i18nReady;
  useEffect(() => {
    if (ready) document.documentElement.removeAttribute("data-branding");
  }, [ready]);
  return null;
}
