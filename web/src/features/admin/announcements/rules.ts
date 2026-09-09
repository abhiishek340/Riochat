/** Pure rules of the announcement screens. */

export const ANNOUNCEMENT_TYPES = ["general", "normal", "info", "warning", "critical"] as const;
export type AnnouncementType = (typeof ANNOUNCEMENT_TYPES)[number];

export interface Announcement {
  id: number;
  title: string;
  contentMarkdown: string;
  status: "active" | "inactive" | (string & {});
  type: string;
  pinned: boolean;
  priority: number;
  startsAt: string | null;
  expiresAt: string | null;
  createdByUserID: number;
  closedAt: string | null;
  createdAt: string;
  updatedAt: string;
}

/** Unknown types fall back to `general`. */
export function normalizeType(type: string | null | undefined): AnnouncementType {
  return (ANNOUNCEMENT_TYPES as readonly string[]).includes(type ?? "") ? (type as AnnouncementType) : "general";
}

/** Visible right now: active, started (or no start) and not expired (or no end). */
export function isCurrentlyVisible(a: Pick<Announcement, "status" | "startsAt" | "expiresAt">, now: number = Date.now()): boolean {
  if (a.status !== "active") return false;
  if (a.startsAt && Date.parse(a.startsAt) > now) return false;
  if (a.expiresAt && Date.parse(a.expiresAt) <= now) return false;
  return true;
}

export type PriorityInput = { kind: "blank" } | { kind: "invalid" } | { kind: "ok"; value: number };

/** Inline priority edits: blank discards, junk is an error, digits (optionally signed) commit. */
export function parsePriorityInput(text: string): PriorityInput {
  const value = text.trim();
  if (value === "") return { kind: "blank" };
  if (!/^[+-]?\d+$/.test(value)) return { kind: "invalid" };
  const n = Number(value);
  return Number.isSafeInteger(n) ? { kind: "ok", value: n } : { kind: "invalid" };
}

/** Priority in the form: non-numeric becomes 0. */
export function priorityFromForm(text: string): number {
  const parsed = parsePriorityInput(text);
  return parsed.kind === "ok" ? parsed.value : 0;
}

function localDay(text: string, h: number, m: number, s: number, ms: number): string | null {
  const match = /^(\d{4})-(\d{2})-(\d{2})$/.exec(text.trim());
  if (!match) return null;
  const date = new Date(Number(match[1]), Number(match[2]) - 1, Number(match[3]), h, m, s, ms);
  return Number.isNaN(date.getTime()) ? null : date.toISOString();
}

/** Start date means 00:00:00 local; empty means no start. */
export function startsAtFromDate(text: string): string | null {
  return text.trim() ? localDay(text, 0, 0, 0, 0) : null;
}

/** End date means 23:59:59 local; empty means no end. */
export function expiresAtFromDate(text: string): string | null {
  return text.trim() ? localDay(text, 23, 59, 59, 0) : null;
}

export function dateFromIso(iso: string | null | undefined): string {
  if (!iso) return "";
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return "";
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, "0")}-${String(d.getDate()).padStart(2, "0")}`;
}

export interface AnnouncementForm {
  title: string;
  content: string;
  type: string;
  priority: string;
  pinned: boolean;
  active: boolean;
  startDate: string;
  endDate: string;
}

export const EMPTY_FORM: AnnouncementForm = { title: "", content: "", type: "general", priority: "0", pinned: false, active: true, startDate: "", endDate: "" };

export function formOf(a: Announcement | null): AnnouncementForm {
  if (!a) return EMPTY_FORM;
  return {
    title: a.title,
    content: a.contentMarkdown,
    type: normalizeType(a.type),
    priority: String(a.priority),
    pinned: a.pinned,
    active: a.status === "active",
    startDate: dateFromIso(a.startsAt),
    endDate: dateFromIso(a.expiresAt),
  };
}

/** Title and content are trimmed; blank after trimming means nothing is sent. */
export function bodyOf(form: AnnouncementForm, original?: Pick<Announcement, "startsAt" | "expiresAt">): { title: string; contentMarkdown: string; status: string; type: string; pinned: boolean; priority: number; startsAt: string | null; expiresAt: string | null } | null {
  const title = form.title.trim();
  const contentMarkdown = form.content.trim();
  if (!title || !contentMarkdown) return null;
  return {
    title,
    contentMarkdown,
    status: form.active ? "active" : "inactive",
    type: normalizeType(form.type),
    pinned: form.pinned,
    priority: priorityFromForm(form.priority),
    // A day that was not touched keeps the exact instant the server holds.
    startsAt: original && form.startDate === dateFromIso(original.startsAt) ? (original.startsAt ?? null) : startsAtFromDate(form.startDate),
    expiresAt: original && form.endDate === dateFromIso(original.expiresAt) ? (original.expiresAt ?? null) : expiresAtFromDate(form.endDate),
  };
}
