"use client";

import { createContext, useCallback, useContext, useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { useIsMobile } from "@/hooks/use-media-query";
import { readBoolean, writeBoolean, writeCookie } from "@/lib/utils/storage";

const STORAGE_KEY = "riochat.sidebar.open";
const COOKIE_NAME = "sidebar_state";
const COLLAPSE_BELOW_PX = 1180;
const RESTORE_AT_PX = 1360;

interface SidebarValue {
  /** Desktop expanded (true) or icon rail (false). */
  open: boolean;
  setOpen: (open: boolean) => void;
  toggle: () => void;
  isMobile: boolean;
  /** Off-canvas sheet on phones. */
  mobileOpen: boolean;
  setMobileOpen: (open: boolean) => void;
}

const Ctx = createContext<SidebarValue | null>(null);

/**
 * Sidebar state.
 *  - persisted per device (localStorage + a 7-day cookie);
 *  - auto-collapses when the window narrows below 1180px and restores at
 *    1360px, but only if that automatic collapse was what closed it;
 *  - Ctrl/Cmd+B toggles; on phones the sidebar is an off-canvas sheet.
 */
export function SidebarProvider({ children }: { children: ReactNode }) {
  const isMobile = useIsMobile();
  const [open, setOpenState] = useState(true);
  const [mobileOpen, setMobileOpen] = useState(false);
  const openRef = useRef(true);
  const autoCollapsed = useRef(false);

  const commit = useCallback((next: boolean) => {
    openRef.current = next;
    setOpenState(next);
  }, []);

  // Manual changes persist and cancel any pending automatic restore.
  const setOpen = useCallback(
    (next: boolean) => {
      autoCollapsed.current = false;
      commit(next);
      writeBoolean(STORAGE_KEY, next);
      writeCookie(COOKIE_NAME, String(next), 60 * 60 * 24 * 7);
    },
    [commit],
  );

  useEffect(() => {
    const stored = readBoolean(STORAGE_KEY, true);
    if (window.innerWidth < COLLAPSE_BELOW_PX) {
      autoCollapsed.current = stored;
      commit(false);
    } else {
      commit(stored);
    }
    const low = window.matchMedia(`(max-width: ${COLLAPSE_BELOW_PX - 1}px)`);
    const high = window.matchMedia(`(min-width: ${RESTORE_AT_PX}px)`);
    const onLow = () => {
      if (low.matches && openRef.current) {
        autoCollapsed.current = true;
        commit(false);
      }
    };
    const onHigh = () => {
      if (high.matches && autoCollapsed.current) {
        autoCollapsed.current = false;
        commit(true);
      }
    };
    low.addEventListener("change", onLow);
    high.addEventListener("change", onHigh);
    return () => {
      low.removeEventListener("change", onLow);
      high.removeEventListener("change", onHigh);
    };
  }, [commit]);

  const toggle = useCallback(() => {
    if (isMobile) setMobileOpen((v) => !v);
    else setOpen(!openRef.current);
  }, [isMobile, setOpen]);

  useEffect(() => {
    const onKey = (event: KeyboardEvent) => {
      if (event.key.toLowerCase() === "b" && (event.metaKey || event.ctrlKey) && !event.altKey && !event.shiftKey) {
        event.preventDefault();
        toggle();
      }
    };
    window.addEventListener("keydown", onKey);
    return () => window.removeEventListener("keydown", onKey);
  }, [toggle]);

  useEffect(() => {
    if (!isMobile) setMobileOpen(false);
  }, [isMobile]);

  const value = useMemo(
    () => ({ open, setOpen, toggle, isMobile, mobileOpen, setMobileOpen }),
    [open, setOpen, toggle, isMobile, mobileOpen],
  );
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useSidebar(): SidebarValue {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useSidebar must be used inside <SidebarProvider>");
  return ctx;
}
