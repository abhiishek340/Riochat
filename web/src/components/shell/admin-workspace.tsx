"use client";

import type { ReactNode } from "react";
import { AdminUpdateMark } from "@/features/admin";
import { ShellExtras } from "@/features/auth-shell-share";
import { ADMIN_NAV } from "@/config/navigation";
import { useT } from "@/lib/i18n/provider";
import { AuthGuard } from "@/lib/session/auth-guard";
import { FeaturePolicyProvider } from "@/lib/session/feature-policy";
import { AdminGate } from "./admin-gate";
import { AppShell } from "./app-shell";

export function AdminWorkspace({ children }: { children: ReactNode }) {
  const t = useT();
  return (
    <AuthGuard>
      <FeaturePolicyProvider>
        <AdminGate>
          <AppShell
            groups={ADMIN_NAV}
            backHref="/chat"
            backLabel={t("shell.admin.backToApp")}
            navLabel={t("shell.admin.title")}
            defaultActiveId="statistics"
            renderMark={(entry) => (entry.mark === "update" ? <AdminUpdateMark /> : null)}
          >
            {children}
          </AppShell>
          <ShellExtras />
        </AdminGate>
      </FeaturePolicyProvider>
    </AuthGuard>
  );
}
