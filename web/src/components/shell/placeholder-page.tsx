"use client";

import { Hammer } from "lucide-react";
import { EmptyState } from "@/components/ui/empty-state";
import { useT, type MessageKey } from "@/lib/i18n/provider";

/**
 * Stand-in screen for an area whose feature code has not been written yet.
 * Feature owners replace the page component exported from their
 * src/features/<area>/index.ts and delete their use of this.
 */
export function PlaceholderPage({ titleKey }: { titleKey: MessageKey }) {
  const t = useT();
  return (
    <div className="mx-auto flex min-h-full max-w-xl items-center justify-center p-6">
      <EmptyState icon={<Hammer />} title={t("shell.placeholder.title", { area: t(titleKey) })} description={t("shell.placeholder.body")} />
    </div>
  );
}
