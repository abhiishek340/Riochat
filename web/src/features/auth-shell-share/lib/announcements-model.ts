import { isRecord, str } from "@/lib/utils/guards";

export const ANNOUNCEMENT_TYPES = ["critical", "warning", "info", "normal", "general"] as const;
export type AnnouncementType = (typeof ANNOUNCEMENT_TYPES)[number];
export type AnnouncementSort = "default" | "type" | "time";

export interface Announcement {
  id: number;
  title: string;
  contentMarkdown: string;
  type: AnnouncementType;
  pinned: boolean;
  priority: number;
  status: string;
  startsAt: string | null;
  expiresAt: string | null;
  createdAt: string;
  updatedAt: string;
  createdByUserID: number;
  closedAt: string | null;
}

const RANK: Record<AnnouncementType, number> = { critical: 5, warning: 4, info: 3, normal: 2, general: 1 };

export function normalizeType(value: unknown): AnnouncementType {
  return (ANNOUNCEMENT_TYPES as readonly unknown[]).includes(value) ? (value as AnnouncementType) : "general";
}

export function typeRank(type: AnnouncementType): number {
  return RANK[type];
}

export function isUnread(item: Pick<Announcement, "closedAt">): boolean {
  return !item.closedAt;
}

/** Tolerant reader for the list endpoint: drops rows without a numeric id. */
export function readAnnouncements(payload: unknown): Announcement[] {
  if (!Array.isArray(payload)) return [];
  const out: Announcement[] = [];
  for (const raw of payload) {
    if (!isRecord(raw) || typeof raw.id !== "number") continue;
    out.push({
      id: raw.id,
      title: str(raw.title),
      contentMarkdown: str(raw.contentMarkdown),
      type: normalizeType(raw.type),
      pinned: raw.pinned === true,
      priority: typeof raw.priority === "number" ? raw.priority : 0,
      status: str(raw.status),
      startsAt: typeof raw.startsAt === "string" ? raw.startsAt : null,
      expiresAt: typeof raw.expiresAt === "string" ? raw.expiresAt : null,
      createdAt: str(raw.createdAt),
      updatedAt: str(raw.updatedAt),
      createdByUserID: typeof raw.createdByUserID === "number" ? raw.createdByUserID : 0,
      closedAt: typeof raw.closedAt === "string" && raw.closedAt ? raw.closedAt : null,
    });
  }
  return out;
}

function stamp(iso: string): number {
  const n = Date.parse(iso);
  return Number.isNaN(n) ? 0 : n;
}

/** "default" keeps server order; the others put unread first. Never mutates the input. */
export function sortAnnouncements(items: readonly Announcement[], mode: AnnouncementSort): Announcement[] {
  const list = [...items];
  if (mode === "default") return list;
  const unreadFirst = (a: Announcement, b: Announcement) => Number(isUnread(b)) - Number(isUnread(a));
  const newest = (a: Announcement, b: Announcement) => stamp(b.updatedAt) - stamp(a.updatedAt) || b.id - a.id;
  if (mode === "type") {
    return list.sort((a, b) => unreadFirst(a, b) || typeRank(b.type) - typeRank(a.type) || newest(a, b));
  }
  return list.sort((a, b) => unreadFirst(a, b) || newest(a, b));
}

/** `yyyy/mm/dd`, or "-" when the value is not a date. */
export function formatAnnouncementDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "-";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${d.getFullYear()}/${pad(d.getMonth() + 1)}/${pad(d.getDate())}`;
}

export function formatClock(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  const pad = (n: number) => String(n).padStart(2, "0");
  return `${pad(d.getHours())}:${pad(d.getMinutes())}:${pad(d.getSeconds())}`;
}

/** The queue is skipped on public share pages (the viewer may not even be signed in). */
export function announcementsSuppressed(pathname: string): boolean {
  return pathname === "/share" || pathname.startsWith("/share/");
}

export const OPEN_ANNOUNCEMENTS_EVENT = "riochat:announcements-open";
export const ANNOUNCEMENTS_UNREAD_EVENT = "riochat:announcements-unread";

export function requestOpenAnnouncements(): void {
  if (typeof window !== "undefined") window.dispatchEvent(new Event(OPEN_ANNOUNCEMENTS_EVENT));
}

let lastUnread = false;

/** The most recent broadcast, for components that mount after it was sent. */
export function currentUnread(): boolean {
  return lastUnread;
}

export function broadcastUnread(unread: boolean): void {
  lastUnread = unread;
  if (typeof window !== "undefined") window.dispatchEvent(new CustomEvent(ANNOUNCEMENTS_UNREAD_EVENT, { detail: unread }));
}
