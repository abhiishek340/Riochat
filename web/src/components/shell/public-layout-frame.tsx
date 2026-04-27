"use client";

import Link from "next/link";
import type { ReactNode } from "react";
import { AppLogo } from "@/components/brand/logo";
import { useBranding } from "@/lib/branding/provider";
import { LanguageMenu } from "./language-menu";
import { ThemeMenu } from "./theme-menu";

/** Frame for public pages that need no session (shared conversations). */
export function PublicLayoutFrame({ children }: { children: ReactNode }) {
  const { branding } = useBranding();
  return (
    <div className="flex min-h-dvh flex-col">
      <header className="flex h-[var(--header-height)] shrink-0 items-center justify-between border-b px-4 sm:px-6">
        {/* The built-in mark leads home; an operator-supplied logo stays a plain image. */}
        {branding.logoURL ? (
          <div className="flex items-center gap-2.5">
            <AppLogo className="size-7" />
            <span className="text-[15px] font-semibold tracking-tight">{branding.shortName || branding.title}</span>
          </div>
        ) : (
          <Link href="/" className="flex items-center gap-2.5 rounded-md outline-none focus-visible:ring-2 focus-visible:ring-ring">
            <AppLogo className="size-7" />
            <span className="text-[15px] font-semibold tracking-tight">{branding.shortName || branding.title}</span>
          </Link>
        )}
        <div className="flex items-center gap-0.5">
          <LanguageMenu />
          <ThemeMenu />
        </div>
      </header>
      <main className="flex-1">{children}</main>
    </div>
  );
}
