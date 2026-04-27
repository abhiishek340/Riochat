"use client";

import Link from "next/link";
import { usePathname, useRouter } from "next/navigation";
import { useEffect, useState, type ReactNode } from "react";
import { isDesktop } from "@/lib/api/platform";
import { useCapabilities } from "@/lib/capabilities/provider";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { useProviderAccess } from "../models/use-provider-access";
import { activeSection, DEFAULT_BASE_PATH, isBlockedByCapability, isBlockedByPlatform, sectionPath, visibleSections } from "./sections";

/**
 * Two-pane frame of every settings page: a section list (a scrolling tab strip
 * on narrow screens) and the page. A section hidden by a server capability or
 * by the platform sends the visitor to General instead.
 */
export function SettingsLayout({ children, basePath = DEFAULT_BASE_PATH }: { children: ReactNode; basePath?: string }) {
  const t = useT();
  const pathname = usePathname() ?? basePath;
  const router = useRouter();
  const { flags, loaded } = useCapabilities();
  const { access } = useProviderAccess();
  const [hydrated, setHydrated] = useState(false);
  const [desktop, setDesktop] = useState(false);

  useEffect(() => {
    setDesktop(isDesktop());
    setHydrated(true);
  }, []);

  const sections = visibleSections({ flags, providerAccess: access ? access.enabled : null, desktop });
  const active = activeSection(pathname, basePath);

  // Capability rules wait for the capabilities, the desktop rule for hydration.
  const blockedNow = (loaded && isBlockedByCapability(pathname, flags, basePath)) || (hydrated && isBlockedByPlatform(pathname, desktop, basePath));

  useEffect(() => {
    if (blockedNow) router.replace(`${basePath}/general`);
  }, [blockedNow, router, basePath]);

  return (
    <div className="mx-auto flex min-h-full w-full max-w-[1230px] flex-col md:flex-row">
      <aside className="sticky top-0 z-10 shrink-0 border-b bg-background/95 backdrop-blur md:static md:w-56 md:border-0 md:bg-transparent md:py-8 md:pl-6 md:backdrop-blur-none">
        <nav
          aria-label={t("settings.navLabel")}
          className="flex gap-1 overflow-x-auto px-4 py-2 md:grid md:content-start md:overflow-visible md:p-0"
        >
          <h2 className="hidden px-2.5 pb-2 text-xs font-semibold uppercase tracking-wider text-muted-foreground md:block">{t("settings.title")}</h2>
          {sections.map((section) => {
            const Icon = section.icon;
            const current = section.id === active.id;
            return (
              <Link
                key={section.id}
                href={sectionPath(section, basePath)}
                prefetch={false}
                aria-current={current ? "page" : undefined}
                className={cn(
                  "flex h-9 shrink-0 items-center gap-2.5 whitespace-nowrap rounded-lg px-2.5 text-sm font-medium text-muted-foreground outline-none transition-colors hover:bg-accent hover:text-accent-foreground focus-visible:ring-2 focus-visible:ring-ring",
                  current && "bg-accent text-accent-foreground",
                )}
              >
                <Icon className="size-[18px] shrink-0" />
                {t(section.labelKey)}
              </Link>
            );
          })}
        </nav>
      </aside>
      <div className="min-w-0 flex-1 px-4 py-6 md:max-w-[1080px] md:px-8 md:py-8">{blockedNow ? null : children}</div>
    </div>
  );
}
