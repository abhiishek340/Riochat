/** Activity heatmap rules: streaks, levels and the week grid. */
import type { ActivityDay } from "./api";

export type Metric = "tokens" | "requests";

export interface Streaks {
  longest: number;
  current: number;
  activeDays: number;
}

/** A day is active when it has at least one request. Days must be sorted ascending. */
export function computeStreaks(days: readonly ActivityDay[]): Streaks {
  let longest = 0;
  let run = 0;
  let activeDays = 0;
  for (const d of days) {
    if (d.requestCount > 0) {
      run++;
      activeDays++;
      longest = Math.max(longest, run);
    } else {
      run = 0;
    }
  }
  // The last day is still going: skip it when empty and count back from the day before.
  let end = days.length - 1;
  if (end >= 0 && (days[end] as ActivityDay).requestCount <= 0) end--;
  let current = 0;
  for (let i = end; i >= 0; i--) {
    if ((days[i] as ActivityDay).requestCount > 0) current++;
    else break;
  }
  return { longest, current, activeDays };
}

export function peakTokens(days: readonly ActivityDay[]): number {
  return days.reduce((m, d) => Math.max(m, d.tokenUsage), 0);
}

/** 0 for no activity, else 1..4. Tokens scale to the peak day, requests in steps of five. */
export function cellLevel(value: number, metric: Metric, peak: number): 0 | 1 | 2 | 3 | 4 {
  if (!(value > 0)) return 0;
  const raw = metric === "tokens" ? (peak > 0 ? Math.ceil((value / peak) * 4) : 1) : Math.ceil(value / 5);
  return Math.min(4, Math.max(1, raw)) as 1 | 2 | 3 | 4;
}

export interface Cell {
  date: string;
  requestCount: number;
  tokenUsage: number;
}

/** Sunday-aligned columns of seven rows; `null` pads before the first and after the last day. */
export function buildWeeks(days: readonly ActivityDay[]): (Cell | null)[][] {
  if (days.length === 0) return [];
  const first = new Date(`${days[0]?.date}T00:00:00`);
  const lead = Number.isNaN(first.getTime()) ? 0 : first.getDay();
  const cells: (Cell | null)[] = [...Array<null>(lead).fill(null), ...days];
  const weeks: (Cell | null)[][] = [];
  for (let i = 0; i < cells.length; i += 7) {
    const week = cells.slice(i, i + 7);
    while (week.length < 7) week.push(null);
    weeks.push(week);
  }
  return weeks;
}

export interface MonthMark {
  column: number;
  month: number;
  year: number;
}

/** A mark where each month first appears; a crowded first mark is dropped. */
export function monthMarks(weeks: readonly (Cell | null)[][]): MonthMark[] {
  const marks: MonthMark[] = [];
  let lastKey = "";
  weeks.forEach((week, column) => {
    for (const cell of week) {
      if (!cell) continue;
      const [y, m] = cell.date.split("-");
      const key = `${y}-${m}`;
      if (key !== lastKey) {
        marks.push({ column, month: Number(m) - 1, year: Number(y) });
        lastKey = key;
      }
      break;
    }
  });
  if (marks.length > 1 && (marks[1] as MonthMark).column - (marks[0] as MonthMark).column < 3) marks.shift();
  return marks;
}

export function totalRequests(days: readonly ActivityDay[]): number {
  return days.reduce((sum, d) => sum + d.requestCount, 0);
}

export function hasAnyActivity(days: readonly ActivityDay[]): boolean {
  return totalRequests(days) > 0 || peakTokens(days) > 0;
}
