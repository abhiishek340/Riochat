import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { isRecord, num, str } from "@/lib/utils/guards";

export const SKILL_LIMITS = { title: 64, trigger: 64, description: 256, markdown: 10000 } as const;

export interface SkillSummary {
  id: number;
  scope: string;
  title: string;
  trigger: string;
  description: string;
  enabled: boolean;
  sortOrder: number;
}

export interface SkillFull extends SkillSummary {
  markdown: string;
}

export interface SkillInput {
  title: string;
  trigger: string;
  description: string;
  markdown: string;
}

function summary(raw: unknown): SkillSummary | null {
  if (!isRecord(raw)) return null;
  return {
    id: num(raw.id),
    scope: str(raw.scope),
    title: str(raw.title),
    trigger: str(raw.trigger),
    description: str(raw.description),
    enabled: raw.enabled !== false,
    sortOrder: num(raw.sortOrder),
  };
}

function full(raw: unknown): SkillFull | null {
  const s = summary(raw);
  return s && isRecord(raw) ? { ...s, markdown: str(raw.markdown) } : null;
}

function results(data: unknown): SkillSummary[] {
  const list = isRecord(data) && Array.isArray(data.results) ? data.results : [];
  return list.flatMap((x) => {
    const s = summary(x);
    return s ? [s] : [];
  });
}

export async function listMine(signal?: AbortSignal): Promise<SkillSummary[]> {
  return results(await api.get<unknown>("/skills/mine", { query: { page_size: 200 }, signal }));
}

/** Skills visible to the user (built-in and own, enabled ones). */
export async function discover(signal?: AbortSignal): Promise<SkillSummary[]> {
  return results(await api.get<unknown>("/skills", { query: { page_size: 200 }, signal }));
}

export async function getSkill(id: number, signal?: AbortSignal): Promise<SkillFull | null> {
  const data = await api.get<unknown>(`/skills/${seg(id)}`, { signal });
  return full(isRecord(data) ? data.skill : null);
}

export async function createSkill(input: SkillInput): Promise<SkillFull | null> {
  const data = await api.post<unknown>("/skills/mine", input);
  return full(isRecord(data) ? data.skill : null);
}

export async function patchSkill(id: number, patch: Partial<SkillInput> & { enabled?: boolean }): Promise<SkillFull | null> {
  const data = await api.patch<unknown>(`/skills/mine/${seg(id)}`, patch);
  return full(isRecord(data) ? data.skill : null);
}

export async function removeSkill(id: number): Promise<void> {
  await api.delete(`/skills/mine/${seg(id)}`);
}

/** Leading slashes and surrounding blanks do not belong to a trigger. */
export function normalizeTrigger(text: string): string {
  return text.trim().replace(/^\/+/, "").trim();
}

export function validateSkill(input: SkillInput): boolean {
  const len = (s: string) => Array.from(s).length;
  const title = input.title.trim();
  const trigger = normalizeTrigger(input.trigger);
  const markdown = input.markdown.trim();
  return (
    len(title) >= 1 &&
    len(title) <= SKILL_LIMITS.title &&
    len(trigger) >= 1 &&
    len(trigger) <= SKILL_LIMITS.trigger &&
    len(input.description.trim()) <= SKILL_LIMITS.description &&
    len(markdown) >= 1 &&
    len(markdown) <= SKILL_LIMITS.markdown
  );
}
