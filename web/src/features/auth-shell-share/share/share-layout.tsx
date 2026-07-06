"use client";

import type { ReactNode } from "react";
import { AppShell } from "@/components/shell/app-shell";
import { LoadingScreen } from "@/components/shell/loading-screen";
import { PublicLayoutFrame } from "@/components/shell/public-layout-frame";
import { APP_NAV } from "@/config/navigation";
import { useT } from "@/lib/i18n/provider";
import { FeaturePolicyProvider } from "@/lib/session/feature-policy";
import { ShellExtras } from "../shell/shell-extras";
import { useViewer } from "./use-viewer";

/**
 * The share route's frame. Signed-in viewers get the full app shell (so they
 * can keep navigating); everyone else sees the bare public frame. Until the
 * session question is answered nothing is mounted, so the page does not
 * remount when the answer arrives.
 */
export function ShareLayout({ children }: { children: ReactNode }) {
  const t = useT();
  const viewer = useViewer();
  if (!viewer.resolved) return <LoadingScreen label={t("common.states.loading")} />;
  if (!viewer.signedIn) return <PublicLayoutFrame>{children}</PublicLayoutFrame>;
  return (
    <FeaturePolicyProvider>
      <AppShell groups={APP_NAV}>{children}</AppShell>
      <ShellExtras />
    </FeaturePolicyProvider>
  );
}
