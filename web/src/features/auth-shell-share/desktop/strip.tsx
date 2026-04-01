"use client";

import { Copy, Globe, Laptop, Minus, Plus, Square, X } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState, type PointerEvent as ReactPointerEvent, type ReactNode } from "react";
import { cn } from "@/lib/utils/cn";
import { invoke, listen } from "./bridge";
import {
  arrangeTabs,
  DRAG_THRESHOLD_PX,
  dropIndex,
  isClosable,
  readSnapshot,
  reorderIds,
  stripStrings,
  tabIcon,
  tabLabel,
  tabTooltip,
  type StripSnapshot,
  type StripTab,
} from "./strip-model";

interface Drag {
  id: string;
  startX: number;
  active: boolean;
}

const EMPTY: StripSnapshot = { tabs: [], active: null, platform: "linux" };

function report(error: unknown): void {
  console.warn("tab strip:", error);
}

/**
 * The browser-like tab strip. It only talks to the shell (`tabs_*` commands
 * and the `tabs:changed` event) and holds no state of its own beyond the last
 * snapshot and an in-progress drag.
 */
export function TabStrip() {
  const [snapshot, setSnapshot] = useState<StripSnapshot>(EMPTY);
  const [dragging, setDragging] = useState<string | null>(null);
  // Order shown right after a drop, until the shell confirms with its own snapshot.
  const [pendingOrder, setPendingOrder] = useState<string[] | null>(null);
  const [maximized, setMaximized] = useState(false);
  const strings = useMemo(() => stripStrings(), []);
  const tabs = useMemo(() => arrangeTabs(snapshot.tabs, pendingOrder), [snapshot.tabs, pendingOrder]);
  const drag = useRef<Drag | null>(null);
  const tabEls = useRef(new Map<string, HTMLElement>());

  useEffect(() => {
    // This page skips the app providers, so it lifts the "branding pending" veil itself.
    document.documentElement.removeAttribute("data-branding");
    let live = true;
    let off: (() => void) | undefined;
    const apply = (raw: unknown) => {
      const next = readSnapshot(raw);
      if (!live || !next) return;
      setSnapshot(next);
      setPendingOrder(null);
    };
    void listen("tabs:changed", apply).then((unlisten) => {
      if (live) off = unlisten;
      else unlisten();
    });
    invoke("tabs_list").then(apply, report);
    return () => {
      live = false;
      off?.();
    };
  }, []);

  // Windows only: the maximize button swaps its icon when the window is resized or maximized.
  const windows = snapshot.platform === "windows";
  useEffect(() => {
    if (!windows) return;
    let live = true;
    let off: (() => void) | undefined;
    const refresh = () => {
      invoke("plugin:window|is_maximized").then(
        (value) => live && setMaximized(value === true),
        () => undefined,
      );
    };
    refresh();
    void listen("tauri://resize", refresh).then((unlisten) => {
      if (live) off = unlisten;
      else unlisten();
    });
    return () => {
      live = false;
      off?.();
    };
  }, [windows]);

  const activate = useCallback((id: string, focus: boolean) => {
    invoke("tabs_activate", { id, focus }).catch(report);
  }, []);

  const onPointerDown = (event: ReactPointerEvent<HTMLDivElement>, id: string) => {
    if (event.button !== 0) return;
    drag.current = { id, startX: event.clientX, active: false };
    event.currentTarget.setPointerCapture(event.pointerId);
    // Moving focus to the content webview mid-press would cancel the drag.
    activate(id, false);
  };

  const onPointerMove = (event: ReactPointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    if (!d || d.active || Math.abs(event.clientX - d.startX) < DRAG_THRESHOLD_PX) return;
    d.active = true;
    setDragging(d.id);
  };

  const onPointerUp = (event: ReactPointerEvent<HTMLDivElement>) => {
    const d = drag.current;
    drag.current = null;
    setDragging(null);
    if (!d) return;
    if (!d.active) {
      activate(d.id, true);
      return;
    }
    const centers: number[] = [];
    for (const tab of tabs) {
      const el = tabEls.current.get(tab.id);
      if (!el || tab.id === d.id) continue;
      const box = el.getBoundingClientRect();
      centers.push(box.left + box.width / 2);
    }
    const index = dropIndex(centers, event.clientX);
    const ids = tabs.map((t) => t.id);
    const current = ids.indexOf(d.id);
    if (index !== current) {
      setPendingOrder(reorderIds(ids, d.id, index));
      invoke("tabs_move", { id: d.id, index }).catch((error: unknown) => {
        setPendingOrder(null);
        report(error);
      });
    }
    activate(d.id, true);
  };

  const close = (id: string) => void invoke("tabs_close", { id }).catch(report);
  const open = () => void invoke("tabs_open").catch(report);

  return (
    <div
      className={cn(
        "flex h-[38px] select-none items-stretch overflow-hidden border-b border-sidebar-border bg-sidebar text-sidebar-foreground",
        snapshot.platform === "macos" && "pl-[78px]",
      )}
      data-tauri-drag-region
    >
      <div className="flex min-w-0 items-end gap-0.5 px-1.5 pt-1.5" role="tablist" data-tauri-drag-region>
        {tabs.map((tab) => {
          const selected = tab.id === snapshot.active;
          const closable = isClosable(tab, tabs.length);
          return (
            <div
              key={tab.id}
              ref={(el) => {
                if (el) tabEls.current.set(tab.id, el);
                else tabEls.current.delete(tab.id);
              }}
              role="tab"
              aria-selected={selected}
              tabIndex={selected ? 0 : -1}
              title={tabTooltip(tab, strings)}
              onPointerDown={(e) => onPointerDown(e, tab.id)}
              onAuxClick={(e) => {
                // Middle click closes, like a browser tab.
                if (e.button !== 1) return;
                e.preventDefault();
                if (closable) close(tab.id);
              }}
              onKeyDown={(e) => {
                if (e.key === "Enter" || e.key === " ") {
                  e.preventDefault();
                  activate(tab.id, true);
                } else if (e.key === "Delete" && closable) {
                  e.preventDefault();
                  close(tab.id);
                }
              }}
              onPointerMove={onPointerMove}
              onPointerUp={onPointerUp}
              onPointerCancel={() => {
                drag.current = null;
                setDragging(null);
              }}
              className={cn(
                "group relative flex h-[30px] min-w-[96px] max-w-[220px] flex-1 items-center gap-1.5 rounded-t-lg px-3 text-[13px]",
                selected ? "bg-background text-foreground" : "text-muted-foreground hover:bg-sidebar-accent/60",
                dragging === tab.id && "opacity-70",
              )}
            >
              <TabGlyph tab={tab} />
              <span className="min-w-0 flex-1 truncate">{tabLabel(tab, strings)}</span>
              {closable ? (
                <button
                  type="button"
                  aria-label={strings.closeTab}
                  title={strings.closeTab}
                  onPointerDown={(e) => e.stopPropagation()}
                  onClick={() => close(tab.id)}
                  className={cn(
                    "grid size-4 shrink-0 place-items-center rounded hover:bg-sidebar-accent focus-visible:opacity-100",
                    selected ? "opacity-70" : "opacity-0 group-hover:opacity-70",
                  )}
                >
                  <X className="size-3" />
                </button>
              ) : null}
            </div>
          );
        })}
        <button
          type="button"
          aria-label={strings.newTabHint}
          title={strings.newTabHint}
          onClick={open}
          className="mb-0.5 ml-0.5 grid size-7 shrink-0 place-items-center rounded-md text-muted-foreground hover:bg-sidebar-accent"
        >
          <Plus className="size-4" />
        </button>
      </div>
      <div className="min-w-8 flex-1" data-tauri-drag-region />
      {windows ? (
        <div className="flex items-stretch">
          <WindowButton label={strings.minimize} command="plugin:window|minimize" icon={<Minus className="size-4" />} />
          <WindowButton
            label={maximized ? strings.restore : strings.maximize}
            command="plugin:window|toggle_maximize"
            icon={maximized ? <Copy className="size-3" /> : <Square className="size-3" />}
          />
          <WindowButton label={strings.closeWindow} command="plugin:window|close" icon={<X className="size-4" />} danger />
        </div>
      ) : null}
    </div>
  );
}

function TabGlyph({ tab }: { tab: StripTab }) {
  const icon = tabIcon(tab);
  const cls = "size-3.5 shrink-0";
  if (icon === "plus") return <Plus aria-hidden className={cls} />;
  return icon === "laptop" ? <Laptop aria-hidden className={cls} /> : <Globe aria-hidden className={cls} />;
}

function WindowButton({ label, command, icon, danger }: { label: string; command: string; icon: ReactNode; danger?: boolean }) {
  return (
    <button
      type="button"
      aria-label={label}
      title={label}
      onClick={() => void invoke(command).catch(report)}
      className={cn("grid w-11 place-items-center text-muted-foreground hover:text-foreground", danger ? "hover:bg-destructive hover:text-destructive-foreground" : "hover:bg-sidebar-accent")}
    >
      {icon}
    </button>
  );
}
