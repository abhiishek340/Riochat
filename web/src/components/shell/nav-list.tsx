"use client";

import Link from "next/link";
import { useEffect, useRef, type ReactNode } from "react";
import { usePathname } from "next/navigation";
import { isAvailable, useCapabilities } from "@/lib/capabilities/provider";
import { useSession } from "@/lib/session/provider";
import { useFeaturePolicy } from "@/lib/session/feature-policy";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { isActivePath, type NavEntry, type NavGroup } from "@/config/navigation";
import { Tooltip } from "@/components/ui/tooltip";

export function NavList({ groups, collapsed, onNavigate, label: navLabel, renderMark, defaultActiveId }: { groups: NavGroup[]; collapsed: boolean; onNavigate?: () => void; label?: string; renderMark?: (entry: NavEntry) => ReactNode; defaultActiveId?: string }) {
  const t = useT();
  const pathname = usePathname();
  const { flags } = useCapabilities();
  const { policy } = useFeaturePolicy();
  const { user } = useSession();
  const nav = useRef<HTMLElement>(null);

  // The active entry scrolls into view when the active section changes (a strip on narrow screens).
  const entries = groups.flatMap((g) => g.items);
  // A path that belongs to no entry highlights the default one, when the shell names it.
  const activeId = (entries.find((e) => isActivePath(pathname, e.href)) ?? entries.find((e) => e.id === defaultActiveId))?.id;
  const activeHref = entries.find((e) => e.id === activeId)?.href;
  useEffect(() => {
    const node = nav.current?.querySelector<HTMLElement>('[aria-current="page"]');
    node?.scrollIntoView?.({ block: "nearest", inline: "center" });
  }, [activeHref]);

  return (
    <nav ref={nav} aria-label={navLabel ?? t("shell.nav.primary")} className="grid gap-4">
      {groups.map((group) => {
        const items = group.items.filter((e) => isAvailable(e, flags) && (!e.role || user?.role === e.role) && (!e.policy || policy[e.policy]));
        if (!items.length) return null;
        return (
          <div key={group.id} className="grid gap-0.5">
            {group.labelKey && !collapsed ? (
              <p className="px-2.5 pb-1 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{t(group.labelKey)}</p>
            ) : null}
            {items.map((entry) => {
              const active = entry.id === activeId;
              const label = t(entry.labelKey);
              const Icon = entry.icon;
              return (
                <Tooltip key={entry.id} label={collapsed ? label : ""} side="right">
                  <Link
                    href={entry.href}
                    prefetch={false}
                    onClick={onNavigate}
                    aria-current={active ? "page" : undefined}
                    aria-label={collapsed ? label : undefined}
                    className={cn(
                      "flex h-9 items-center gap-2.5 rounded-lg px-2.5 text-sm font-medium text-sidebar-foreground/80 outline-none transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground focus-visible:ring-2 focus-visible:ring-ring",
                      active && "bg-sidebar-accent text-sidebar-accent-foreground",
                      collapsed && "justify-center px-0",
                    )}
                  >
                    <Icon className="size-[18px] shrink-0" />
                    {collapsed ? null : <span className="truncate">{label}</span>}
                    {collapsed ? null : renderMark?.(entry)}
                  </Link>
                </Tooltip>
              );
            })}
          </div>
        );
      })}
    </nav>
  );
}
