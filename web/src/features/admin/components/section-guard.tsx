"use client";

import type { ReactNode } from "react";
import { EmptyState } from "@/components/ui/empty-state";
import { ShieldAlert } from "lucide-react";
import type { FeatureName } from "@/lib/capabilities/features";
import { useSectionGuard } from "@/lib/capabilities/use-section-guard";
import { useT } from "@/lib/i18n/provider";
import { useSession } from "@/lib/session/provider";

export const DEFAULT_ADMIN_HREF = "/admin/statistics";

/** True for the one role that may manage protected resources. */
export function useIsSuperAdmin(): boolean {
  const { user } = useSession();
  return user?.role === "superadmin";
}

/**
 * Wraps one console section: redirects away while the section's server
 * capability is off and, when `superOnly`, answers non-super admins with a
 * notice instead of the content (so no request is made on their behalf).
 */
export function AdminSection({ feature, superOnly = false, children }: { feature?: FeatureName; superOnly?: boolean; children: ReactNode }) {
  const t = useT();
  const guard = useSectionGuard(feature, DEFAULT_ADMIN_HREF);
  const { user, status } = useSession();
  if (guard === "blocked") return null;
  if (superOnly) {
    if (status === "loading" || !user) return null;
    if (user.role !== "superadmin") {
      return (
        <div className="mx-auto flex max-w-xl items-center justify-center p-6">
          <EmptyState icon={<ShieldAlert />} title={t("adminCommon.page.notAllowedTitle")} description={t("adminCommon.page.notAllowedBody")} />
        </div>
      );
    }
  }
  return <>{children}</>;
}
