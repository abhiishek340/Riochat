/** Pure rules of the "all conversations" page: filters, list merging, grouping, bulk planning. */
import { isRecord, str } from "@/lib/utils/guards";

export const PAGE_SIZE = 50;

export type StatusFilter = "all" | "active" | "archived";
export type StarFilter = "all" | "starred" | "unstarred";
export type ShareFilter = "all" | "shared" | "unshared";

export interface Filters {
  status: StatusFilter;
  starred: StarFilter;
  share: ShareFilter;
  /** "all", "unassigned" or a project public id. */
  project: string;
  /** Already normalised (trimmed, case-folded). */
  query: string;
}

export const DEFAULT_FILTERS: Filters = { status: "all", starred: "all", share: "all", project: "all", query: "" };

export interface ConversationRow {
  publicID: string;
  title: string;
  status: string;
  isStarred: boolean;
  labels: string[];
  projectID: string;
  projectName: string;
  shareStatus: string;
  shareID: string;
  sharedAt: string | null;
  lastShareAccessedAt: string | null;
  updatedAt: string;
  createdAt: string;
  model: string;
  messageCount: number;
}

function parseLabels(raw: unknown): string[] {
  if (typeof raw !== "string" || !raw.trim()) return [];
  try {
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.filter((l): l is string => typeof l === "string" && l.trim() !== "") : [];
  } catch {
    return [];
  }
}

function optionalDate(value: unknown): string | null {
  return typeof value === "string" && value ? value : null;
}

export function readConversation(raw: unknown): ConversationRow | null {
  if (!isRecord(raw)) return null;
  const publicID = str(raw.publicID);
  if (!publicID) return null;
  return {
    publicID,
    title: str(raw.title),
    status: str(raw.status) || "active",
    isStarred: raw.isStarred === true,
    labels: parseLabels(raw.labelsJSON),
    projectID: str(raw.projectID),
    projectName: str(raw.projectName),
    shareStatus: str(raw.shareStatus),
    shareID: str(raw.shareID),
    sharedAt: optionalDate(raw.sharedAt),
    lastShareAccessedAt: optionalDate(raw.lastShareAccessedAt),
    updatedAt: str(raw.updatedAt),
    createdAt: str(raw.createdAt),
    model: str(raw.model),
    messageCount: typeof raw.messageCount === "number" ? raw.messageCount : 0,
  };
}

/** Shared means an active share with a share id. */
export function isShared(row: Pick<ConversationRow, "shareStatus" | "shareID">): boolean {
  return row.shareStatus === "active" && row.shareID.trim() !== "";
}

export function isArchived(row: Pick<ConversationRow, "status">): boolean {
  return row.status === "archived";
}

/** Does the row satisfy the structural filters? (The text query is the server's business.) */
export function matchesFilters(row: ConversationRow, f: Filters): boolean {
  if (f.status === "archived" && !isArchived(row)) return false;
  if (f.status === "active" && isArchived(row)) return false;
  if (f.starred === "starred" && !row.isStarred) return false;
  if (f.starred === "unstarred" && row.isStarred) return false;
  if (f.share === "shared" && !isShared(row)) return false;
  if (f.share === "unshared" && isShared(row)) return false;
  if (f.project === "unassigned" && row.projectID) return false;
  if (f.project !== "all" && f.project !== "unassigned" && row.projectID !== f.project) return false;
  return true;
}

export function filtersKey(f: Filters): string {
  return [f.status, f.starred, f.share, f.project, f.query].join("\u0001");
}

export function listQuery(f: Filters, page: number): Record<string, string | number | undefined> {
  return {
    page,
    page_size: PAGE_SIZE,
    status: f.status,
    starred: f.starred,
    share: f.share,
    project: f.project,
    q: f.query || undefined,
  };
}

function stamp(iso: string): number {
  const n = Date.parse(iso);
  return Number.isNaN(n) ? 0 : n;
}

export function sortByUpdated(rows: readonly ConversationRow[]): ConversationRow[] {
  return [...rows].sort((a, b) => stamp(b.updatedAt) - stamp(a.updatedAt));
}

/** Later pages are appended; a row already on screen keeps its position. */
export function mergePage(current: readonly ConversationRow[], more: readonly ConversationRow[]): ConversationRow[] {
  const seen = new Set(current.map((r) => r.publicID));
  const out = [...current];
  for (const row of more) {
    if (!seen.has(row.publicID)) {
      seen.add(row.publicID);
      out.push(row);
    }
  }
  return out;
}
