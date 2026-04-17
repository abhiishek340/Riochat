"use client";

import Link from "next/link";
import { usePathname } from "next/navigation";
import { useCallback, type ReactNode } from "react";
import * as DialogPrimitive from "@radix-ui/react-dialog";
import { ArrowLeft, PanelLeft } from "lucide-react";
import { AppLogo } from "@/components/brand/logo";
import { Button } from "@/components/ui/button";
import { DialogOverlay, DialogPortal } from "@/components/ui/dialog";
import { Tooltip } from "@/components/ui/tooltip";
import { SearchTrigger } from "@/features/auth-shell-share";
import { findNavEntry, type NavEntry, type NavGroup } from "@/config/navigation";
import { useBranding } from "@/lib/branding/provider";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { HeaderProvider, useHeader } from "./header-context";
import { LanguageMenu } from "./language-menu";
import { NavList } from "./nav-list";
import { SidebarProvider, useSidebar } from "./sidebar-context";
import { ThemeMenu } from "./theme-menu";
import { UserMenu } from "./user-menu";

export interface AppShellProps {
  /** Navigation groups for this shell (see src/config/navigation.ts). */
  groups: NavGroup[];
  /** Feature-owned sidebar content (for example the conversation list), shown under the nav. */
  sidebarSlot?: ReactNode;
  /** When set, the sidebar header shows a "back" link instead of the plain brand (admin console). */
  backHref?: string;
  backLabel?: string;
  /** Accessible name of the navigation landmark; defaults to the generic main navigation label. */
  navLabel?: string;
  /** Extra content after an entry's label (status marks such as "update available"). */
  renderMark?: (entry: NavEntry) => ReactNode;
  /** The entry highlighted when the current path belongs to none of them (the admin console's default section). */
  defaultActiveId?: string;
  children: ReactNode;
}

export function AppShell(props: AppShellProps) {
  return (
    <SidebarProvider>
      <HeaderProvider>
        <ShellFrame {...props} />
      </HeaderProvider>
    </SidebarProvider>
  );
}

function ShellFrame({ groups, sidebarSlot, backHref, backLabel, navLabel, renderMark, defaultActiveId, children }: AppShellProps) {
  const t = useT();
  const { open, isMobile, mobileOpen, setMobileOpen } = useSidebar();
  const body = (collapsed: boolean, onNavigate?: () => void) => (
    <SidebarBody
      groups={groups}
      collapsed={collapsed}
      slot={sidebarSlot}
      backHref={backHref}
      backLabel={backLabel}
      navLabel={navLabel}
      renderMark={renderMark}
      defaultActiveId={defaultActiveId}
      onNavigate={onNavigate}
    />
  );

  return (
    <div className="flex h-dvh overflow-hidden bg-background">
      <a
        href="#main"
        className="sr-only z-[200] rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground focus:not-sr-only focus:fixed focus:left-3 focus:top-3"
      >
        {t("shell.sidebar.skipToContent")}
      </a>

      {isMobile ? (
        <DialogPrimitive.Root open={mobileOpen} onOpenChange={setMobileOpen}>
          <DialogPortal>
            <DialogOverlay />
            <DialogPrimitive.Content
              aria-describedby={undefined}
              className="fixed inset-y-0 left-0 z-50 flex w-[min(20rem,85vw)] flex-col border-r bg-sidebar text-sidebar-foreground shadow-lg outline-none data-[state=open]:animate-[rio-slide-in-left_220ms_cubic-bezier(0.22,1,0.36,1)] data-[state=closed]:animate-[rio-slide-out-left_160ms_ease-in]"
            >
              <DialogPrimitive.Title className="sr-only">{t("shell.nav.primary")}</DialogPrimitive.Title>
              {body(false, () => setMobileOpen(false))}
            </DialogPrimitive.Content>
          </DialogPortal>
        </DialogPrimitive.Root>
      ) : (
        <aside
          data-state={open ? "expanded" : "rail"}
          style={{ width: open ? "var(--sidebar-width)" : "var(--sidebar-rail-width)" }}
          className="flex shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground transition-[width] duration-200 ease-linear motion-reduce:transition-none"
        >
          {body(!open)}
        </aside>
      )}

      <div className="flex min-w-0 flex-1 flex-col">
        <ShellHeader groups={groups} />
        <main id="main" tabIndex={-1} className="min-h-0 flex-1 overflow-auto outline-none">
          {children}
        </main>
      </div>
    </div>
  );
}

function SidebarBody({
  groups,
  collapsed,
  slot,
  backHref,
  backLabel,
  navLabel,
  renderMark,
  defaultActiveId,
  onNavigate,
}: {
  groups: NavGroup[];
  collapsed: boolean;
  slot?: ReactNode;
  backHref?: string;
  backLabel?: string;
  navLabel?: string;
  renderMark?: (entry: NavEntry) => ReactNode;
  defaultActiveId?: string;
  onNavigate?: () => void;
}) {
  const { branding } = useBranding();
  const { toggle, isMobile } = useSidebar();
  const t = useT();
  return (
    <>
      <div className={cn("flex h-[var(--header-height)] shrink-0 items-center gap-2 px-3", collapsed && "justify-center px-0")}>
        {collapsed ? (
          <Tooltip label={t("shell.sidebar.toggle")} side="right">
            <button
              type="button"
              onClick={toggle}
              aria-label={t("shell.sidebar.toggle")}
              className="rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <AppLogo className="size-8" />
            </button>
          </Tooltip>
        ) : (
          <>
            <Link href="/chat" onClick={onNavigate} className="flex min-w-0 flex-1 items-center gap-2.5 rounded-lg outline-none focus-visible:ring-2 focus-visible:ring-ring">
              <AppLogo className="size-8" />
              <span className="truncate text-[15px] font-semibold tracking-tight">{branding.shortName || branding.title}</span>
            </Link>
            {isMobile ? null : (
              <Tooltip label={t("shell.sidebar.toggle")}>
                <Button variant="ghost" size="icon-sm" onClick={toggle} aria-label={t("shell.sidebar.toggle")}>
                  <PanelLeft />
                </Button>
              </Tooltip>
            )}
          </>
        )}
      </div>

      {backHref && !collapsed ? (
        <div className="px-3 pb-2">
          <Link
            href={backHref}
            onClick={onNavigate}
            className="flex h-8 items-center gap-2 rounded-lg px-2.5 text-[13px] font-medium text-muted-foreground transition-colors hover:bg-sidebar-accent hover:text-sidebar-accent-foreground"
          >
            <ArrowLeft className="size-4" />
            {backLabel}
          </Link>
        </div>
      ) : null}

      <div className={cn("min-h-0 flex-1 overflow-y-auto px-2 pb-2", collapsed && "px-1.5")}>
        <NavList groups={groups} collapsed={collapsed} onNavigate={onNavigate} label={navLabel} renderMark={renderMark} defaultActiveId={defaultActiveId} />
        {collapsed || !slot ? null : <div className="mt-4 border-t border-sidebar-border pt-3">{slot}</div>}
      </div>

      <div className={cn("shrink-0 border-t border-sidebar-border p-2", collapsed && "px-1.5")}>
        <UserMenu collapsed={collapsed} />
      </div>
    </>
  );
}

function ShellHeader({ groups }: { groups: NavGroup[] }) {
  const t = useT();
  const pathname = usePathname();
  const { toggle, isMobile } = useSidebar();
  const { title, setActionsTarget } = useHeader();
  const entry = groups.flatMap((g) => g.items).find((e) => findNavEntry(pathname)?.id === e.id);
  const heading = title ?? (entry ? t(entry.labelKey) : "");
  const actionsRef = useCallback((el: HTMLDivElement | null) => setActionsTarget(el), [setActionsTarget]);

  return (
    <header className="flex h-[var(--header-height)] shrink-0 items-center gap-2 border-b bg-background/80 px-3 backdrop-blur sm:px-4">
      {isMobile ? (
        <Button variant="ghost" size="icon-sm" onClick={toggle} aria-label={t("shell.sidebar.openMenu")}>
          <PanelLeft />
        </Button>
      ) : null}
      <h1 className="min-w-0 flex-1 truncate text-[15px] font-semibold tracking-tight">{heading}</h1>
      <div ref={actionsRef} className="flex items-center gap-1.5 empty:hidden" />
      <div className="flex items-center gap-0.5">
        <SearchTrigger />
        <LanguageMenu />
        <ThemeMenu />
      </div>
    </header>
  );
}
