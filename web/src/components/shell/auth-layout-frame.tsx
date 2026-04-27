"use client";

import type { ReactNode } from "react";
import { AppLogo } from "@/components/brand/logo";
import { useBranding } from "@/lib/branding/provider";
import { LanguageMenu } from "./language-menu";
import { ThemeMenu } from "./theme-menu";

export function AuthLayoutFrame({ children }: { children: ReactNode }) {
  const { branding } = useBranding();
  return (
    <div className="relative flex min-h-dvh flex-col overflow-hidden">
      {/* Soft river-light wash behind the card. */}
      <div
        aria-hidden
        className="pointer-events-none absolute inset-0 -z-10 opacity-70 [background:radial-gradient(60rem_32rem_at_15%_-10%,color-mix(in_oklab,var(--primary)_16%,transparent),transparent),radial-gradient(50rem_30rem_at_100%_110%,color-mix(in_oklab,var(--chart-2)_14%,transparent),transparent)]"
      />
      <header className="flex items-center justify-between px-5 py-4 sm:px-8">
        <div className="flex items-center gap-2.5">
          <AppLogo className="size-8" />
          <span className="text-[15px] font-semibold tracking-tight">{branding.shortName || branding.title}</span>
        </div>
        <div className="flex items-center gap-0.5">
          <LanguageMenu />
          <ThemeMenu />
        </div>
      </header>
      <main className="flex flex-1 items-center justify-center px-4 pb-16 pt-4">
        <div className="w-full max-w-sm rounded-2xl border bg-card p-6 shadow-md sm:p-8">{children}</div>
      </main>
    </div>
  );
}
