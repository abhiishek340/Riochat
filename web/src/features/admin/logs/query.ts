/** Query building shared by the log tabs. */
import { endOfDayIso, startOfDayIso } from "../lib/format";

export const LOG_TABS = ["audit", "usage", "auth", "orders", "redemptions", "conversation", "moderation", "errors"] as const;
export type LogTab = (typeof LOG_TABS)[number];

export type CleanupType = "audit" | "auth" | "usage" | "orders" | "conversation" | "moderation" | "errors";

/** Tabs whose records can be purged; redemption records are financial and never are. */
export const CLEANUP_OF: Record<LogTab, CleanupType | null> = {
  audit: "audit",
  usage: "usage",
  auth: "auth",
  orders: "orders",
  redemptions: null,
  conversation: "conversation",
  moderation: "moderation",
  errors: "errors",
};

export interface TabAccess {
  multiUser: boolean;
  billingGating: boolean;
  contentModeration: boolean;
  superAdmin: boolean;
}

export function visibleTabs(access: TabAccess): LogTab[] {
  return LOG_TABS.filter((tab) => {
    if (tab === "auth") return access.multiUser;
    if (tab === "orders" || tab === "redemptions") return access.billingGating;
    if (tab === "moderation") return access.contentModeration && access.superAdmin;
    return true;
  });
}

/** The tab named by the URL when it is visible, otherwise `audit`. */
export function initialTab(requested: string | null | undefined, visible: readonly LogTab[]): LogTab {
  return (visible as readonly string[]).includes(requested ?? "") ? (requested as LogTab) : "audit";
}

/** A positive integer from text such as a `code_id` query value. */
export function positiveInt(value: string | null | undefined): number | undefined {
  const text = (value ?? "").trim();
  if (!/^\d+$/.test(text)) return undefined;
  const n = Number(text);
  return n > 0 && Number.isSafeInteger(n) ? n : undefined;
}

/**
 * Search text that is all digits names a user; anything else is a text query.
 * `idKey` is the parameter the digits go to.
 */
export function searchParams(text: string, idKey: string, textKey: string | null = "query"): Record<string, string | undefined> {
  const value = text.trim();
  if (!value) return {};
  if (/^\d+$/.test(value)) return { [idKey]: value };
  return textKey ? { [textKey]: value } : {};
}

/** The dates picked as days become instants spanning those local days. */
export function dateRange(from: string, to: string): { created_from?: string; created_to?: string } {
  const out: { created_from?: string; created_to?: string } = {};
  const start = startOfDayIso(from);
  const end = endOfDayIso(to);
  if (start) out.created_from = start;
  if (end) out.created_to = end;
  return out;
}

/** Cutoff for a cleanup: the chosen day at 00:00 local; no future days. */
export function cleanupBefore(day: string, now: Date = new Date()): string | null {
  const iso = startOfDayIso(day);
  if (!iso) return null;
  const start = new Date(now.getFullYear(), now.getMonth(), now.getDate(), 0, 0, 0, 0).getTime();
  if (Date.parse(iso) > start) return null;
  return iso;
}

/** Distinct values of one field, alphabetical, blanks dropped. */
export function distinct(values: ReadonlyArray<string | null | undefined>): string[] {
  return [...new Set(values.map((v) => (v ?? "").trim()).filter(Boolean))].sort((a, b) => a.localeCompare(b));
}

/** Known options first, then anything else seen in the data. */
export function withObserved(known: readonly string[], observed: readonly string[]): string[] {
  return [...known, ...observed.filter((v) => !known.includes(v))];
}

export type AuthSort = "occurred_desc" | "occurred_asc" | "id_desc" | "id_asc";

export function sortAuthEvents<T extends { id: number; occurredAt: string }>(rows: readonly T[], sort: AuthSort): T[] {
  const list = [...rows];
  const at = (r: T) => Date.parse(r.occurredAt) || 0;
  switch (sort) {
    case "occurred_asc":
      return list.sort((a, b) => at(a) - at(b) || a.id - b.id);
    case "id_desc":
      return list.sort((a, b) => b.id - a.id);
    case "id_asc":
      return list.sort((a, b) => a.id - b.id);
    default:
      return list.sort((a, b) => at(b) - at(a) || b.id - a.id);
  }
}

export const AUDIT_RESOURCES = ["user", "conversation", "conversation_project", "message", "file", "memory", "settings", "logs"] as const;
export const AUDIT_ACTIONS = [
  "login",
  "stream_message",
  "create_conversation",
  "fork_conversation",
  "rename_conversation",
  "delete_conversation",
  "update_conversation_labels",
  "export_conversation",
  "set_conversation_star",
  "set_conversation_archive",
  "set_conversation_project",
  "batch_set_conversation_project",
  "create_conversation_project",
  "update_conversation_project",
  "delete_conversation_project",
  "reorder_conversation_projects",
  "create_conversation_share",
  "regenerate_conversation_share",
  "revoke_conversation_share",
  "clone_shared_conversation",
  "update_message",
  "set_message_feedback",
  "upload_file",
  "update_file",
  "delete_file",
  "upsert_user_memory",
  "delete_user_memory",
  "settings.update",
  "admin_create_user",
  "admin_patch_user",
  "admin_update_user_status",
  "admin_revoke_user_sessions",
  "admin_reset_user_password",
  "admin_reset_user_2fa",
  "admin_delete_user",
  "admin_cleanup_logs",
] as const;
