/** Grouping and searching models for the picker and the @ menu. */
import type { ModelDTO } from "../types";

export interface ModelGroup {
  id: string;
  name: string;
  icon: string;
  byok: boolean;
  models: ModelDTO[];
}

const OTHER = "__other__";

/** Platform groups by name (display order of their models), then personal providers. */
export function groupModels(models: readonly ModelDTO[], otherLabel: string): ModelGroup[] {
  const platform = new Map<string, ModelGroup>();
  const personal = new Map<string, ModelGroup>();
  for (const m of models) {
    if (m.source === "personal" || (m.providerName && m.source !== "platform")) {
      const key = m.providerName || OTHER;
      let g = personal.get(key);
      if (!g) personal.set(key, (g = { id: `byok:${key}`, name: m.providerName || otherLabel, icon: m.providerIcon, byok: true, models: [] }));
      g.models.push(m);
      continue;
    }
    const key = m.displayGroupID !== null ? `g${m.displayGroupID}` : m.displayGroupName || m.vendorName || OTHER;
    let g = platform.get(key);
    if (!g) platform.set(key, (g = { id: key, name: m.displayGroupName || m.vendorName || otherLabel, icon: m.displayGroupIcon || m.vendorIcon, byok: false, models: [] }));
    g.models.push(m);
  }
  const bySort = (a: ModelDTO, b: ModelDTO) => a.sortOrder - b.sortOrder || a.platformModelName.localeCompare(b.platformModelName);
  const finish = (g: ModelGroup) => ({ ...g, models: [...g.models].sort(bySort) });
  const firstSort = (g: ModelGroup) => Math.min(...g.models.map((m) => m.sortOrder));
  return [
    ...[...platform.values()].map(finish).sort((a, b) => firstSort(a) - firstSort(b) || a.name.localeCompare(b.name)),
    ...[...personal.values()].map(finish).sort((a, b) => a.name.localeCompare(b.name)),
  ];
}

export function matchesModel(m: ModelDTO, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return [m.platformModelName, m.vendorName, m.vendor, m.displayGroupName, m.description, m.providerName].some((s) => s.toLowerCase().includes(q));
}

export function modalitiesOf(m: ModelDTO): string[] {
  const order = ["text", "image", "pdf", "audio", "video"];
  const list = (m.inputModalities ?? []).filter((x) => order.includes(x));
  return list.length > 0 ? order.filter((x) => list.includes(x)) : ["text"];
}

export function formatContext(tokens: number | null): string {
  if (!tokens || tokens <= 0) return "";
  if (tokens >= 1_000_000) return `${Number((tokens / 1_000_000).toFixed(2))}M`;
  if (tokens >= 1000) return `${Math.round(tokens / 1000)}K`;
  return String(tokens);
}

/** Index of the schedule period in effect now (weekday and time in the period's UTC offset), else -1. */
export function currentPeriod(
  periods: readonly { weekdays: number[]; start: string; end: string }[],
  offsetMinutes: number,
  now: Date,
): number {
  const shifted = new Date(now.getTime() + offsetMinutes * 60_000);
  const weekday = shifted.getUTCDay() === 0 ? 7 : shifted.getUTCDay();
  const minutes = shifted.getUTCHours() * 60 + shifted.getUTCMinutes();
  const toMin = (s: string) => {
    const [h, m] = s.split(":").map(Number);
    return (h ?? 0) * 60 + (m ?? 0);
  };
  return periods.findIndex((p) => {
    if (p.weekdays.length > 0 && !p.weekdays.includes(weekday)) return false;
    const start = toMin(p.start);
    const end = toMin(p.end);
    return start <= end ? minutes >= start && minutes < end : minutes >= start || minutes < end;
  });
}
