"use client";

import Link from "next/link";
import { FileQuestion } from "lucide-react";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { useT } from "@/lib/i18n/provider";

export default function NotFound() {
  const t = useT();
  return (
    <div className="flex min-h-dvh items-center justify-center p-6">
      <EmptyState
        icon={<FileQuestion />}
        title={t("shell.notFound.title")}
        description={t("shell.notFound.body")}
        action={
          <Button asChild>
            <Link href="/chat">{t("shell.notFound.home")}</Link>
          </Button>
        }
      />
    </div>
  );
}
