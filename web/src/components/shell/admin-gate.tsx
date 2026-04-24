"use client";

import Link from "next/link";
import { ShieldAlert } from "lucide-react";
import type { ReactNode } from "react";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Spinner } from "@/components/ui/spinner";
import { useT } from "@/lib/i18n/provider";
import { isAdminRole, useSession } from "@/lib/session/provider";

/**
 * Admin routes render only for administrators. The server enforces this too;
 * the gate just avoids showing screens that would answer 403 for every call.
 */
export function AdminGate({ children }: { children: ReactNode }) {
  const t = useT();
  const { user, status } = useSession();
  if (status === "loading" || (status === "failed" && !user)) {
    return (
      <div role="status" className="flex h-dvh items-center justify-center text-muted-foreground">
        <Spinner className="size-5" />
      </div>
    );
  }
  if (!isAdminRole(user?.role)) {
    return (
      <div className="flex h-dvh items-center justify-center p-6">
        <EmptyState
          icon={<ShieldAlert />}
          title={t("shell.admin.forbiddenTitle")}
          description={t("shell.admin.forbiddenBody")}
          action={
            <Button asChild variant="outline">
              <Link href="/chat">{t("shell.admin.backToApp")}</Link>
            </Button>
          }
        />
      </div>
    );
  }
  return <>{children}</>;
}
