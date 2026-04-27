"use client";

import type { ReactNode } from "react";
import { ConversationSidebar } from "@/features/chat";
import { ShellExtras } from "@/features/auth-shell-share";
import { APP_NAV } from "@/config/navigation";
import { AuthGuard } from "@/lib/session/auth-guard";
import { FeaturePolicyProvider } from "@/lib/session/feature-policy";
import { AppShell } from "./app-shell";

export function AppWorkspace({ children }: { children: ReactNode }) {
  return (
    <AuthGuard>
      <FeaturePolicyProvider>
        <AppShell groups={APP_NAV} sidebarSlot={<ConversationSidebar />}>
          {children}
        </AppShell>
        <ShellExtras />
      </FeaturePolicyProvider>
    </AuthGuard>
  );
}
