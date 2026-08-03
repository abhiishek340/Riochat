/** Reasoning declarations: formats, levels and how a level reaches the upstream. */
import { isPlainObject } from "../lib/json";

export const REASONING_FORMATS = ["openai", "anthropic_effort", "anthropic_budget", "gemini_level", "gemini_budget", "qwen", "toggle"] as const;
export type ReasoningFormat = (typeof REASONING_FORMATS)[number];

/** Canonical order, weakest first. */
export const LEVELS = ["none", "minimal", "low", "medium", "high", "xhigh", "max", "ultra"] as const;
export type Level = (typeof LEVELS)[number];

export interface ReasoningDecl {
  format: string;
  levels: string[];
  default: string;
  budgets?: Record<string, number>;
}

export function isLevel(value: string): value is Level {
  return (LEVELS as readonly string[]).includes(value);
}

export function sortLevels(levels: readonly string[]): string[] {
  return [...new Set(levels)].sort((a, b) => LEVELS.indexOf(a as Level) - LEVELS.indexOf(b as Level));
}

/**
 * The level used when the admin picks none: `medium` if offered, else the
 * highest level below medium that is not `none`, else the lowest level that is
 * not `none`, else the first level.
 */
export function defaultLevel(levels: readonly string[]): string {
  const sorted = sortLevels(levels);
  if (sorted.includes("medium")) return "medium";
  const mediumAt = LEVELS.indexOf("medium");
  const below = sorted.filter((l) => l !== "none" && LEVELS.indexOf(l as Level) < mediumAt);
  if (below.length > 0) return below[below.length - 1] as string;
  const real = sorted.filter((l) => l !== "none");
  return real[0] ?? sorted[0] ?? "";
}

/** A declaration is invalid when the format or levels are unusable or the default is not among the levels. */
export function isValidDecl(decl: ReasoningDecl): boolean {
  if (!(REASONING_FORMATS as readonly string[]).includes(decl.format)) return false;
  if (decl.levels.length === 0 || !decl.levels.every(isLevel)) return false;
  return (decl.levels as string[]).includes(decl.default);
}

export function usesBudgets(format: string): boolean {
  return format === "anthropic_budget" || format === "gemini_budget" || format === "qwen";
}

/** Budgets only matter for budget formats and only for the selected levels. */
export function sanitizeBudgets(format: string, levels: readonly string[], budgets: Record<string, number> | undefined): Record<string, number> | undefined {
  if (!usesBudgets(format) || !budgets) return undefined;
  const out: Record<string, number> = {};
  for (const level of levels) {
    const n = budgets[level];
    if (typeof n === "number" && Number.isFinite(n) && n >= 0) out[level] = Math.floor(n);
  }
  return Object.keys(out).length > 0 ? out : undefined;
}

/** What the upstream receives for a level, as shown to the admin. */
export function translateLevel(format: string, level: string, budgets?: Record<string, number>): string {
  const budget = budgets?.[level];
  switch (format) {
    case "openai":
    case "gemini_level":
      return level;
    case "anthropic_effort":
      return level === "none" ? "disabled" : level;
    case "gemini_budget":
      return level === "none" ? "0" : budget !== undefined ? String(budget) : "";
    case "anthropic_budget":
      return level === "none" ? "disabled" : budget !== undefined ? String(budget) : "enabled";
    case "qwen":
      return level === "none" ? "disabled" : budget !== undefined ? String(budget) : "enabled";
    case "toggle":
      return level === "none" ? "disabled" : "enabled";
    default:
      return level;
  }
}

export function readDecl(value: unknown): ReasoningDecl | null {
  if (!isPlainObject(value)) return null;
  const levels = Array.isArray(value.levels) ? value.levels.filter((l): l is string => typeof l === "string") : [];
  const budgets = isPlainObject(value.budgets)
    ? Object.fromEntries(Object.entries(value.budgets).filter((e): e is [string, number] => typeof e[1] === "number"))
    : undefined;
  return { format: typeof value.format === "string" ? value.format : "", levels, default: typeof value.default === "string" ? value.default : "", ...(budgets ? { budgets } : {}) };
}

/** The value to store: levels in canonical order, budgets limited to them. */
export function declToValue(decl: ReasoningDecl): Record<string, unknown> {
  const levels = sortLevels(decl.levels);
  const out: Record<string, unknown> = { format: decl.format, levels, default: decl.default || defaultLevel(levels) };
  const budgets = sanitizeBudgets(decl.format, levels, decl.budgets);
  if (budgets) out.budgets = budgets;
  return out;
}

export const MODALITIES = ["text", "image", "pdf", "audio", "video"] as const;

/** Custom modalities always include text. */
export function modalitiesValue(chosen: readonly string[]): string[] {
  const set = new Set(["text", ...chosen.filter((m) => (MODALITIES as readonly string[]).includes(m))]);
  return MODALITIES.filter((m) => set.has(m));
}
