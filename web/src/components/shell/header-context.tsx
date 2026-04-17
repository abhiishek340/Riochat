"use client";

import { createContext, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { createPortal } from "react-dom";

interface HeaderValue {
  title: string | null;
  setTitle: (title: string | null) => void;
  actionsTarget: HTMLElement | null;
  setActionsTarget: (el: HTMLElement | null) => void;
}

const Ctx = createContext<HeaderValue | null>(null);

export function HeaderProvider({ children }: { children: ReactNode }) {
  const [title, setTitle] = useState<string | null>(null);
  const [actionsTarget, setActionsTarget] = useState<HTMLElement | null>(null);
  const value = useMemo(() => ({ title, setTitle, actionsTarget, setActionsTarget }), [title, actionsTarget]);
  return <Ctx.Provider value={value}>{children}</Ctx.Provider>;
}

export function useHeader(): HeaderValue {
  const ctx = useContext(Ctx);
  if (!ctx) throw new Error("useHeader must be used inside the app shell");
  return ctx;
}

/** Overrides the shell header's title while the calling page is mounted. */
export function usePageTitle(title: string | null): void {
  const { setTitle } = useHeader();
  useEffect(() => {
    setTitle(title);
    return () => setTitle(null);
  }, [title, setTitle]);
}

/**
 * Renders its children into the shell header's action area, so a page can put
 * its own buttons next to the global controls.
 *   <HeaderActions><Button>New</Button></HeaderActions>
 */
export function HeaderActions({ children }: { children: ReactNode }) {
  const { actionsTarget } = useHeader();
  return actionsTarget ? createPortal(children, actionsTarget) : null;
}
