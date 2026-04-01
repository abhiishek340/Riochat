/**
 * Pure helpers for the desktop tab strip (`/desktop/tabs`): reading the
 * shell's snapshot, labelling tabs and deciding where a dragged tab lands.
 * The strip page loads without the i18n bundles, so its few strings live here.
 */
import { isRecord } from "@/lib/utils/guards";

export type StripPlatform = "macos" | "windows" | "linux";

export interface StripServer {
  mode: "local" | "remote";
  origin: string;
}

export interface StripTab {
  id: string;
  server: StripServer | null;
  title: string;
}

export interface StripSnapshot {
  tabs: StripTab[];
  active: string | null;
  platform: StripPlatform;
}

export interface StripStrings {
  newTab: string;
  newTabHint: string;
  localServer: string;
  closeTab: string;
  minimize: string;
  maximize: string;
  restore: string;
  closeWindow: string;
}

const EN: StripStrings = {
  newTab: "New tab",
  newTabHint: "Open a new tab",
  localServer: "This computer",
  closeTab: "Close tab",
  minimize: "Minimize",
  maximize: "Maximize",
  restore: "Restore",
  closeWindow: "Close",
};

/** The strip's labels (English only). */
export function stripStrings(): StripStrings {
  return EN;
}

function readServer(raw: unknown): StripServer | null {
  if (!isRecord(raw)) return null;
  const mode = raw.mode === "local" || raw.mode === "remote" ? raw.mode : null;
  return mode ? { mode, origin: typeof raw.origin === "string" ? raw.origin : "" } : null;
}

/** Validates the payload of `tabs_list` / the `tabs:changed` event. */
export function readSnapshot(raw: unknown): StripSnapshot | null {
  if (!isRecord(raw) || !Array.isArray(raw.tabs)) return null;
  const tabs: StripTab[] = [];
  for (const item of raw.tabs) {
    if (!isRecord(item) || typeof item.id !== "string") return null;
    tabs.push({ id: item.id, server: readServer(item.server), title: typeof item.title === "string" ? item.title : "" });
  }
  const platform: StripPlatform = raw.platform === "macos" || raw.platform === "windows" ? raw.platform : "linux";
  return { tabs, active: typeof raw.active === "string" ? raw.active : null, platform };
}

/** "Local" for the bundled server, otherwise the host of the remote origin. */
export function serverLabel(server: StripServer | null, strings: StripStrings): string {
  if (!server) return "";
  if (server.mode === "local") return strings.localServer;
  try {
    return new URL(server.origin).host;
  } catch {
    return server.origin;
  }
}

/** What the tab shows: "This computer", the remote host, or "New tab" for a tab bound to nothing. */
export function tabLabel(tab: StripTab, strings: StripStrings): string {
  return serverLabel(tab.server, strings) || strings.newTab;
}

/** Hover text: "<page title> - <label>" once the page reported a title, otherwise just the label. */
export function tabTooltip(tab: StripTab, strings: StripStrings): string {
  const label = tabLabel(tab, strings);
  const title = tab.title.trim();
  return title ? `${title} \u2014 ${label}` : label;
}

export type TabIcon = "plus" | "laptop" | "globe";

export function tabIcon(tab: StripTab): TabIcon {
  if (!tab.server) return "plus";
  return tab.server.mode === "local" ? "laptop" : "globe";
}

/** A tab can be closed when it is not the only one, or when it is bound to a server. */
export function isClosable(tab: StripTab, tabCount: number): boolean {
  return tabCount > 1 || tab.server !== null;
}

/** Ids in their order after moving `id` so it ends up at `index` of the final list. */
export function reorderIds(ids: readonly string[], id: string, index: number): string[] {
  const rest = ids.filter((x) => x !== id);
  if (rest.length === ids.length) return [...ids];
  rest.splice(Math.min(Math.max(index, 0), rest.length), 0, id);
  return rest;
}

/** Tabs arranged by a (possibly stale) id order; unknown ids sort last in their shell order. */
export function arrangeTabs(tabs: readonly StripTab[], order: readonly string[] | null): StripTab[] {
  if (!order) return [...tabs];
  const rank = new Map(order.map((id, i) => [id, i] as const));
  return [...tabs].sort((a, b) => (rank.get(a.id) ?? order.length) - (rank.get(b.id) ?? order.length));
}

/**
 * Where a dragged tab lands. `centers` are the horizontal centres of every
 * other tab in order; the result is the index in the final order.
 */
export function dropIndex(centers: readonly number[], pointerX: number): number {
  let index = 0;
  for (const c of centers) if (c < pointerX) index++;
  return index;
}

/** A pointer must move this far before a press turns into a drag. */
export const DRAG_THRESHOLD_PX = 5;

/** The strip route is served without the app providers. */
export const STRIP_ROUTE = "/desktop/tabs";

export function isStripRoute(pathname: string | null): boolean {
  if (!pathname) return false;
  const path = pathname.replace(/\.html$/, "").replace(/\/+$/, "");
  return path === STRIP_ROUTE;
}
