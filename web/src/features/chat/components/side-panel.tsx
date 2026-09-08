"use client";

import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import { useIsMobile } from "@/hooks/use-media-query";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";

const MIN_RATIO = 1 / 3;
const MAX_RATIO = 3 / 4;

export function defaultRatio(width: number): number {
  return width >= 1280 ? 1 / 3 : 1 / 2;
}

export function clampRatio(r: number): number {
  return Math.min(MAX_RATIO, Math.max(MIN_RATIO, r));
}

/**
 * The area beside the conversation. Inline it is a resizable column (a third of
 * the workspace on wide screens, half otherwise; drag between a third and
 * three quarters, double-click resets). Under 768 px it covers the whole area.
 */
export function SidePanelFrame({ children, containerRef }: { children: ReactNode; containerRef: React.RefObject<HTMLDivElement | null> }) {
  const t = useT();
  const mobile = useIsMobile();
  const [ratio, setRatio] = useState<number | null>(null);
  const [dragging, setDragging] = useState(false);
  const [viewport, setViewport] = useState(typeof window === "undefined" ? 1280 : window.innerWidth);
  const state = useRef<{ x: number; start: number } | null>(null);

  useEffect(() => {
    const on = () => setViewport(window.innerWidth);
    window.addEventListener("resize", on);
    return () => window.removeEventListener("resize", on);
  }, []);

  const effective = ratio ?? defaultRatio(viewport);

  const finish = useCallback(() => {
    state.current = null;
    setDragging(false);
    document.body.style.removeProperty("cursor");
    document.body.style.removeProperty("user-select");
  }, []);

  useEffect(() => {
    if (!dragging) return;
    // Pointer capture normally ends the drag; these cover losing the pointer in other ways.
    window.addEventListener("blur", finish);
    window.addEventListener("pagehide", finish);
    return () => {
      window.removeEventListener("blur", finish);
      window.removeEventListener("pagehide", finish);
    };
  }, [dragging, finish]);

  useEffect(() => finish, [finish]);

  if (mobile) {
    return <div className="absolute inset-0 z-30 flex flex-col bg-background animate-[rio-slide-in-right_200ms_cubic-bezier(0.22,1,0.36,1)]" role="dialog" aria-modal="false" aria-label={t("chatRich.panel.title")}>{children}</div>;
  }

  return (
    <>
      <div
        role="separator"
        aria-orientation="vertical"
        aria-label={t("chatRich.panel.resize")}
        aria-valuemin={Math.round(MIN_RATIO * 100)}
        aria-valuemax={Math.round(MAX_RATIO * 100)}
        aria-valuenow={Math.round(effective * 100)}
        tabIndex={0}
        data-dragging={dragging}
        className="rio-resize"
        onPointerDown={(e) => {
          const box = containerRef.current?.getBoundingClientRect();
          if (!box) return;
          e.currentTarget.setPointerCapture(e.pointerId);
          state.current = { x: e.clientX, start: effective };
          setDragging(true);
          document.body.style.cursor = "col-resize";
          document.body.style.userSelect = "none";
        }}
        onPointerMove={(e) => {
          const s = state.current;
          const box = containerRef.current?.getBoundingClientRect();
          if (!s || !box) return;
          // The panel is on the right: moving left makes it wider.
          setRatio(clampRatio(s.start + (s.x - e.clientX) / box.width));
        }}
        onPointerUp={finish}
        onPointerCancel={finish}
        onLostPointerCapture={finish}
        onDoubleClick={() => setRatio(null)}
        onKeyDown={(e) => {
          if (e.key === "ArrowLeft") setRatio(clampRatio(effective + 0.02));
          else if (e.key === "ArrowRight") setRatio(clampRatio(effective - 0.02));
          else if (e.key === "Home") setRatio(null);
        }}
      />
      <aside className={cn("flex min-w-0 flex-col border-l bg-background", dragging && "pointer-events-none")} style={{ width: `${effective * 100}%` }}>
        {children}
      </aside>
    </>
  );
}
