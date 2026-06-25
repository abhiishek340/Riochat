/**
 * Public share snapshots: mapping the wire payload to a view model, picking
 * the visible branch, and rewriting owner file URLs to the share-scoped ones.
 */
import { isRecord, num, str } from "@/lib/utils/guards";

export interface ShareMessage {
  id: string;
  parentId: string;
  sourceId: string;
  role: string;
  content: string;
  contentType: string;
  status: string;
  branchReason: "default" | "retry" | "edit";
  errorMessage: string;
  attachments: ShareAttachment[];
  modelName: string;
  modelVendor: string;
  inputTokens: number;
  outputTokens: number;
  cacheReadTokens: number;
  cacheWriteTokens: number;
  reasoningTokens: number;
  latencyMs: number;
  createdAt: string;
}

export interface ShareAttachment {
  fileId: string;
  name: string;
  mime: string;
  kind: string;
  size: number;
}

export interface ShareSnapshot {
  shareId: string;
  title: string;
  model: string;
  createdAt: string;
  uiComponentsEnabled: boolean;
  defaultIds: string[];
  messages: ShareMessage[];
}

/** `/api/v1/files/<id>/content` (preceded by start, whitespace, ( " ' or =) becomes the share-scoped URL. */
export function rewriteSharedFileUrls(text: string, shareId: string): string {
  const share = encodeURIComponent(shareId);
  return text.replace(/(^|[\s("'=])\/api\/v1\/files\/([^/\s)"'?#]+)\/content/g, (_m, lead: string, id: string) => {
    let decoded = id;
    try {
      decoded = decodeURIComponent(id);
    } catch {
      /* keep the raw segment */
    }
    return `${lead}/api/v1/shared-conversations/${share}/files/${encodeURIComponent(decoded)}/content`;
  });
}

export function sharedFileUrl(shareId: string, fileId: string): string {
  return `/api/v1/shared-conversations/${encodeURIComponent(shareId)}/files/${encodeURIComponent(fileId)}/content`;
}

export function parseAttachments(raw: unknown): ShareAttachment[] {
  const parsed: unknown = typeof raw === "string" && raw.trim() ? safeJson(raw) : raw;
  if (!Array.isArray(parsed)) return [];
  const out: ShareAttachment[] = [];
  for (const item of parsed) {
    if (!isRecord(item)) continue;
    const fileId = str(item.file_id);
    if (!fileId) continue;
    out.push({
      fileId,
      name: str(item.file_name) || fileId,
      mime: str(item.detected_mime) || str(item.mime_type),
      kind: str(item.kind),
      size: num(item.file_size),
    });
  }
  return out;
}

function safeJson(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

function mapMessage(raw: Record<string, unknown>, topModel: string): ShareMessage | null {
  const id = str(raw.publicID);
  if (!id) return null;
  const reason = str(raw.branchReason);
  return {
    id,
    parentId: str(raw.parentPublicID),
    sourceId: str(raw.sourcePublicID),
    role: str(raw.role),
    content: str(raw.content),
    contentType: str(raw.contentType),
    status: str(raw.status) || "success",
    branchReason: reason === "retry" || reason === "edit" ? reason : "default",
    errorMessage: str(raw.errorMessage),
    attachments: parseAttachments(raw.attachments),
    modelName: str(raw.platformModelName) || topModel,
    modelVendor: str(raw.modelVendor),
    inputTokens: num(raw.inputTokens),
    outputTokens: num(raw.outputTokens),
    cacheReadTokens: num(raw.cacheReadTokens),
    cacheWriteTokens: num(raw.cacheWriteTokens),
    reasoningTokens: num(raw.reasoningTokens),
    latencyMs: num(raw.latencyMS),
    createdAt: str(raw.createdAt),
  };
}

/** Never surfaces owner request ids or billing: only fields mapped above exist. */
export function readSnapshot(payload: unknown, fallbackShareId: string): ShareSnapshot | null {
  if (!isRecord(payload)) return null;
  const model = str(payload.model);
  const messages = (Array.isArray(payload.messages) ? payload.messages : [])
    .filter(isRecord)
    .map((m) => mapMessage(m, model))
    .filter((m): m is ShareMessage => m !== null);
  return {
    shareId: str(payload.shareID) || fallbackShareId,
    title: str(payload.title),
    model,
    createdAt: str(payload.createdAt),
    uiComponentsEnabled: payload.uiComponentsEnabled === true,
    defaultIds: (Array.isArray(payload.defaultMessagePublicIDs) ? payload.defaultMessagePublicIDs : []).filter(
      (v): v is string => typeof v === "string",
    ),
    messages,
  };
}

export interface BranchIndex {
  byId: Map<string, ShareMessage>;
  /** Sibling lists keyed by parent id; "" holds the roots. Oldest first. */
  children: Map<string, ShareMessage[]>;
}

export function buildBranchIndex(messages: readonly ShareMessage[]): BranchIndex {
  const byId = new Map(messages.map((m) => [m.id, m] as const));
  const children = new Map<string, ShareMessage[]>();
  const ordered = messages
    .map((m, index) => ({ m, index, time: Date.parse(m.createdAt) }))
    .sort((a, b) => (Number.isNaN(a.time) || Number.isNaN(b.time) || a.time === b.time ? a.index - b.index : a.time - b.time));
  for (const { m } of ordered) {
    const parent = m.parentId && byId.has(m.parentId) ? m.parentId : "";
    const list = children.get(parent);
    if (list) list.push(m);
    else children.set(parent, [m]);
  }
  return { byId, children };
}

export function parentKey(index: BranchIndex, message: ShareMessage): string {
  return message.parentId && index.byId.has(message.parentId) ? message.parentId : "";
}

/** Initial choice per parent from the share's default path. */
export function defaultSelection(index: BranchIndex, defaultIds: readonly string[]): Map<string, string> {
  const selection = new Map<string, string>();
  for (const id of defaultIds) {
    const message = index.byId.get(id);
    if (message) selection.set(parentKey(index, message), id);
  }
  return selection;
}

export interface PathEntry {
  message: ShareMessage;
  siblings: ShareMessage[];
  position: number;
}

/**
 * The visible chain from the root. At each level the chosen sibling is the
 * explicit selection when it is still a child there, else the newest one.
 */
export function visiblePath(index: BranchIndex, selection: ReadonlyMap<string, string>): PathEntry[] {
  const path: PathEntry[] = [];
  let parent = "";
  const seen = new Set<string>();
  for (;;) {
    const siblings = index.children.get(parent);
    if (!siblings || siblings.length === 0) break;
    const wanted = selection.get(parent);
    let position = siblings.findIndex((s) => s.id === wanted);
    if (position < 0) position = siblings.length - 1;
    const message = siblings[position];
    if (!message || seen.has(message.id)) break;
    seen.add(message.id);
    path.push({ message, siblings, position });
    parent = message.id;
  }
  return path;
}

/** Previous/next sibling with clamping at both ends (no wraparound). */
export function stepSibling(position: number, delta: -1 | 1, count: number): number {
  return Math.min(Math.max(position + delta, 0), Math.max(count - 1, 0));
}

/** Builds `/login?next=<share page>` for anonymous viewers. */
export function loginForShare(shareId: string): string {
  return `/login?next=${encodeURIComponent(`/share?conversation_id=${encodeURIComponent(shareId)}`)}`;
}
