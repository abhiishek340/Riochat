/**
 * Rules for the tools, skills, knowledge bases and interactive components a
 * message carries: caps, the single image-processor tool, defaults.
 */
import type { McpToolDTO, SkillSummaryDTO, UiComponentDTO } from "../types";

export const DEFAULT_TOOL_CAP = 32;
export const MAX_TOOL_CAP = 128;
export const MAX_KNOWLEDGE_BASES = 8;
export const MAX_UI_COMPONENTS = 32;

export function toolCap(policyValue: number | undefined): number {
  const n = typeof policyValue === "number" && Number.isFinite(policyValue) ? Math.trunc(policyValue) : 0;
  if (n <= 0) return DEFAULT_TOOL_CAP;
  return Math.min(n, MAX_TOOL_CAP);
}

export function isImageProcessor(tool: Pick<McpToolDTO, "attachmentInputMode">): boolean {
  return tool.attachmentInputMode === "image";
}

/** Keeps available ids once, at most one image processor, within the cap. */
export function normalizeTools(ids: readonly number[], available: readonly McpToolDTO[], cap: number): number[] {
  const byId = new Map(available.map((t) => [t.id, t] as const));
  const out: number[] = [];
  let processor = false;
  for (const id of ids) {
    const tool = byId.get(id);
    if (!tool || out.includes(id)) continue;
    if (isImageProcessor(tool)) {
      if (processor) continue;
      processor = true;
    }
    if (out.length >= cap) break;
    out.push(id);
  }
  return out;
}

export type ToggleResult = { ok: true; ids: number[] } | { ok: false; reason: "limit" | "processor" };

export function toggleTool(ids: readonly number[], tool: McpToolDTO, available: readonly McpToolDTO[], cap: number): ToggleResult {
  if (ids.includes(tool.id)) return { ok: true, ids: ids.filter((i) => i !== tool.id) };
  if (ids.length >= cap) return { ok: false, reason: "limit" };
  if (isImageProcessor(tool)) {
    const byId = new Map(available.map((t) => [t.id, t] as const));
    if (ids.some((i) => byId.get(i) && isImageProcessor(byId.get(i) as McpToolDTO))) return { ok: false, reason: "processor" };
  }
  return { ok: true, ids: [...ids, tool.id] };
}

/** Selects or deselects a whole server's tools, stopping at the caps. */
export function toggleServer(ids: readonly number[], serverTools: readonly McpToolDTO[], available: readonly McpToolDTO[], cap: number): { ids: number[]; hitLimit: boolean; skippedProcessor: boolean } {
  const allOn = serverTools.every((t) => ids.includes(t.id));
  if (allOn) {
    const drop = new Set(serverTools.map((t) => t.id));
    return { ids: ids.filter((i) => !drop.has(i)), hitLimit: false, skippedProcessor: false };
  }
  let next = [...ids];
  let hitLimit = false;
  let skippedProcessor = false;
  for (const tool of serverTools) {
    if (next.includes(tool.id)) continue;
    const res = toggleTool(next, tool, available, cap);
    if (res.ok) next = res.ids;
    else if (res.reason === "limit") hitLimit = true;
    else skippedProcessor = true;
  }
  return { ids: next, hitLimit, skippedProcessor };
}

export function toggleSkill(list: readonly SkillSummaryDTO[], skill: SkillSummaryDTO, cap: number): { ok: true; list: SkillSummaryDTO[] } | { ok: false } {
  if (list.some((s) => s.id === skill.id)) return { ok: true, list: list.filter((s) => s.id !== skill.id) };
  if (list.length >= cap) return { ok: false };
  return { ok: true, list: [...list, skill] };
}

export function toggleKnowledgeBase(ids: readonly string[], id: string): { ok: true; ids: string[] } | { ok: false } {
  if (ids.includes(id)) return { ok: true, ids: ids.filter((x) => x !== id) };
  if (ids.length >= MAX_KNOWLEDGE_BASES) return { ok: false };
  return { ok: true, ids: [...ids, id] };
}

// --- interactive components ---------------------------------------------------------------

/** Every enabled built-in component is checked unless the user chose otherwise. */
export function defaultUiComponents(components: readonly UiComponentDTO[]): number[] {
  return components.filter((c) => c.enabled && c.rendererKind === "builtin").map((c) => c.id);
}

/** The stored explicit choice (or null for "defaults") pruned to what is visible. */
export function effectiveUiComponents(components: readonly UiComponentDTO[], stored: readonly number[] | null): number[] {
  const visible = components.filter((c) => c.enabled);
  const ids = new Set(visible.map((c) => c.id));
  const base = stored === null ? defaultUiComponents(components) : [...stored];
  const out: number[] = [];
  for (const id of base) if (ids.has(id) && !out.includes(id)) out.push(id);
  return out.slice(0, MAX_UI_COMPONENTS);
}

/** A choice equal to the defaults is stored as "use defaults". */
export function storableUiChoice(components: readonly UiComponentDTO[], chosen: readonly number[]): number[] | null {
  const defaults = new Set(defaultUiComponents(components));
  const picked = new Set(chosen);
  if (defaults.size === picked.size && [...defaults].every((id) => picked.has(id))) return null;
  return [...chosen];
}

export function parseStoredIds(raw: string | null): number[] | null {
  if (raw === null) return null;
  try {
    const parsed: unknown = JSON.parse(raw);
    if (!Array.isArray(parsed)) return null;
    return parsed.filter((x): x is number => typeof x === "number" && Number.isInteger(x) && x > 0);
  } catch {
    return null;
  }
}
