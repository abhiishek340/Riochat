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

/** More pages exist only if this one was full and fewer than `total` rows are loaded. */
export function hasMorePages(received: number, loaded: number, total: number): boolean {
  return received >= PAGE_SIZE && loaded < total;
}

export interface Section {
  id: string;
  /** Project name, or null for the unassigned ("Default") section. */
  name: string | null;
  rows: ConversationRow[];
}

/**
 * Sections follow the user's project order, then projects the user list does
 * not know (grouped by name or id), then unassigned conversations last.
 */
export function groupByProject(rows: readonly ConversationRow[], projectOrder: readonly { publicID: string; name: string }[]): Section[] {
  const known = new Map(projectOrder.map((p) => [p.publicID, p.name] as const));
  const byProject = new Map<string, ConversationRow[]>();
  const unknown = new Map<string, { name: string; rows: ConversationRow[] }>();
  const unassigned: ConversationRow[] = [];
  for (const row of rows) {
    if (!row.projectID) unassigned.push(row);
    else if (known.has(row.projectID)) byProject.set(row.projectID, [...(byProject.get(row.projectID) ?? []), row]);
    else {
      const key = row.projectName || row.projectID;
      const entry = unknown.get(key) ?? { name: row.projectName || row.projectID, rows: [] };
      entry.rows.push(row);
      unknown.set(key, entry);
    }
  }
  const sections: Section[] = [];
  for (const project of projectOrder) {
    const list = byProject.get(project.publicID);
    if (list?.length) sections.push({ id: project.publicID, name: project.name, rows: list });
  }
  for (const [key, entry] of unknown) sections.push({ id: `unknown:${key}`, name: entry.name, rows: entry.rows });
  if (unassigned.length) sections.push({ id: "unassigned", name: null, rows: unassigned });
  return sections;
}

export type FilterWord = "archived" | "active" | "starred" | "unstarred" | "shared" | "unshared";

/** Words that describe the active filters, for "No archived and shared conversations". */
export function filterWords(f: Filters): FilterWord[] {
  const words: FilterWord[] = [];
  if (f.status !== "all") words.push(f.status);
  if (f.starred !== "all") words.push(f.starred);
  if (f.share !== "all") words.push(f.share);
  return words;
}

export function chunk<T>(items: readonly T[], size: number): T[][] {
  const out: T[][] = [];
  for (let i = 0; i < items.length; i += size) out.push(items.slice(i, i + size));
  return out;
}

export type ArchiveAction = "archive" | "unarchive";

/** Unarchive only when every selected conversation is archived. */
export function archiveActionFor(rows: readonly ConversationRow[]): ArchiveAction {
  return rows.length > 0 && rows.every(isArchived) ? "unarchive" : "archive";
}

/** The rows an archive/unarchive request would actually change. */
export function rowsNeedingChange(rows: readonly ConversationRow[], action: ArchiveAction): ConversationRow[] {
  return rows.filter((r) => (action === "archive" ? !isArchived(r) : isArchived(r)));
}

export interface ExportOutcome {
  kind: "downloaded" | "complete" | "partial";
  exported: number;
  failed: number;
}

function manifestOf(line: string | undefined): ExportOutcome | null {
  if (!line) return null;
  try {
    const parsed: unknown = JSON.parse(line);
    if (isRecord(parsed) && (parsed._type === "export_manifest" || "complete" in parsed)) {
      const exported = typeof parsed.exported === "number" ? parsed.exported : 0;
      const failed = typeof parsed.failed === "number" ? parsed.failed : 0;
      return { kind: parsed.complete === false || failed > 0 ? "partial" : "complete", exported, failed };
    }
  } catch {
    /* that line was a conversation, not a manifest */
  }
  return null;
}

/**
 * The export carries a manifest line, at the start or at the end of the file.
 * Its absence is reported as a plain download; an incomplete one is a warning.
 */
export function readExportManifest(text: string): ExportOutcome {
  const lines = text.split("\n").filter((l) => l.trim() !== "");
  return manifestOf(lines[lines.length - 1]) ?? manifestOf(lines[0]) ?? { kind: "downloaded", exported: 0, failed: 0 };
}

export function exportFileName(now: Date): string {
  return `my-conversations-${now.toISOString().slice(0, 10)}.jsonl`;
}

export type RelativeUnit = "minute" | "hour" | "day" | "month" | "year";

export function relativeParts(iso: string, now: Date): { value: number; unit: RelativeUnit } | null {
  const then = Date.parse(iso);
  if (Number.isNaN(then)) return null;
  const minutes = Math.max(0, Math.floor((now.getTime() - then) / 60_000));
  if (minutes < 60) return { value: minutes, unit: "minute" };
  const hours = Math.floor(minutes / 60);
  if (hours < 24) return { value: hours, unit: "hour" };
  const days = Math.floor(hours / 24);
  if (days < 30) return { value: days, unit: "day" };
  const months = Math.floor(days / 30);
  if (months < 12) return { value: months, unit: "month" };
  return { value: Math.floor(days / 365) || 1, unit: "year" };
}

/** "5 minutes ago" in the app language; null when the date is unreadable. */
export function formatRelative(iso: string, now: Date, locale: string): string | null {
  const parts = relativeParts(iso, now);
  if (!parts) return null;
  return new Intl.RelativeTimeFormat(locale, { numeric: "auto" }).format(-parts.value, parts.unit);
}

/** Selection state: a Set of ids, with the tri-state of the master checkbox. */
export function masterState(selected: ReadonlySet<string>, rows: readonly ConversationRow[]): "none" | "some" | "all" {
  if (rows.length === 0 || selected.size === 0) return "none";
  return rows.every((r) => selected.has(r.publicID)) ? "all" : "some";
}
