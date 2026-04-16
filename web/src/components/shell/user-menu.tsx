"use client";

import Link from "next/link";
import { useRef, useState } from "react";
import { ChevronsUpDown, LogOut, Settings, Shield } from "lucide-react";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { AnnouncementsMenuItem, DownloadDesktopMenuItem, LanguageSubmenu, UpgradePlanMenuItem } from "@/features/auth-shell-share";
import { useT } from "@/lib/i18n/provider";
import { isAdminRole, useSession } from "@/lib/session/provider";
import { absolutizeApiPath } from "@/lib/api/base-url";
import { cn } from "@/lib/utils/cn";

function hueOf(seed: string): number {
  let h = 0;
  for (let i = 0; i < seed.length; i++) h = (h * 31 + seed.charCodeAt(i)) % 360;
  return h;
}

export function Avatar({ name, src, seed, className }: { name: string; src?: string; seed: string; className?: string }) {
  const url = src && (/^https?:\/\//.test(src) || src.startsWith("/api/v1/")) ? absolutizeApiPath(src) : "";
  const initial = Array.from(name.trim())[0]?.toUpperCase() ?? "?";
  if (url) {
    // eslint-disable-next-line @next/next/no-img-element -- avatar URLs come from the backend or an identity provider
    return <img src={url} alt="" className={cn("size-8 shrink-0 rounded-full object-cover", className)} />;
  }
  return (
    <span
      aria-hidden
      className={cn("flex size-8 shrink-0 items-center justify-center rounded-full text-sm font-semibold text-white", className)}
      style={{ background: `oklch(0.55 0.1 ${hueOf(seed)})` }}
    >
      {initial}
    </span>
  );
}

export function UserMenu({ collapsed }: { collapsed: boolean }) {
  const t = useT();
  const { user, signOut } = useSession();
  const [signingOut, setSigningOut] = useState(false);
  const signingOutRef = useRef(false);
  const name = user?.displayName || user?.username || t("shell.user.guest");
  // Repeat clicks while the request runs are ignored.
  const leave = async () => {
    if (signingOutRef.current) return;
    signingOutRef.current = true;
    setSigningOut(true);
    try {
      await signOut();
    } finally {
      signingOutRef.current = false;
      setSigningOut(false);
    }
  };
  return (
    <DropdownMenu>
      <DropdownMenuTrigger
        aria-label={t("shell.user.menu")}
        className={cn(
          "flex w-full items-center gap-2.5 rounded-lg p-1.5 text-left outline-none transition-colors hover:bg-sidebar-accent focus-visible:ring-2 focus-visible:ring-ring data-[state=open]:bg-sidebar-accent",
          collapsed && "justify-center",
        )}
      >
        <Avatar name={name} src={user?.avatarURL} seed={user?.publicID || name} />
        {collapsed ? null : (
          <>
            <span className="min-w-0 flex-1">
              <span className="block truncate text-sm font-medium leading-tight">{name}</span>
              {user?.email ? <span className="block truncate text-xs text-muted-foreground">{user.email}</span> : null}
            </span>
            <ChevronsUpDown className="size-4 shrink-0 text-muted-foreground" />
          </>
        )}
      </DropdownMenuTrigger>
      <DropdownMenuContent side="top" align="start" className="w-60">
        <DropdownMenuLabel>
          {t("shell.user.signedInAs")}
          <span className="block truncate text-sm font-medium text-foreground">{name}</span>
        </DropdownMenuLabel>
        <DropdownMenuSeparator />
        <DropdownMenuItem asChild>
          <Link href="/settings/general">
            <Settings />
            {t("shell.user.settings")}
          </Link>
        </DropdownMenuItem>
        <AnnouncementsMenuItem />
        <LanguageSubmenu />
        <UpgradePlanMenuItem />
        <DownloadDesktopMenuItem />
        {isAdminRole(user?.role) ? (
          <DropdownMenuItem asChild>
            <Link href="/admin">
              <Shield />
              {t("shell.user.admin")}
            </Link>
          </DropdownMenuItem>
        ) : null}
        <DropdownMenuSeparator />
        <DropdownMenuItem
          destructive
          onSelect={(event) => {
            event.preventDefault();
            void leave();
          }}
        >
          <LogOut />
          {signingOut ? t("shellX.menu.signingOut") : t("shell.user.signOut")}
        </DropdownMenuItem>
      </DropdownMenuContent>
    </DropdownMenu>
  );
}
