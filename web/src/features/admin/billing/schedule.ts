/** Time-of-day price multipliers ("schedule pricing"). */

export interface SchedulePeriod {
  name: string;
  /** 0 is Sunday. */
  weekdays: number[];
  start: string;
  end: string;
  /** 100 means 1.0x. */
  ratePercent: number;
}

export const MAX_PERIODS = 32;
export const MIN_RATE = 1;
export const MAX_RATE = 10000;

const FULL_WIDTH_ZERO = 0xff10;

/** Full-width digits and colon become ASCII. */
function narrow(text: string): string {
  return [...text]
    .map((ch) => {
      const code = ch.codePointAt(0) ?? 0;
      if (code >= FULL_WIDTH_ZERO && code <= FULL_WIDTH_ZERO + 9) return String(code - FULL_WIDTH_ZERO);
      return ch === "\uff1a" ? ":" : ch;
    })
    .join("");
}

/**
 * "830" -> "08:30", "8:00" -> "08:00", "24:00" -> "00:00". Anything else that
 * is not a clock time returns null.
 */
export function canonicalizeTime(input: string): string | null {
  const text = narrow(input).trim();
  let h: number;
  let m: number;
  const colon = /^(\d{1,2}):(\d{1,2})$/.exec(text);
  if (colon) {
    h = Number(colon[1]);
    m = Number(colon[2]);
  } else if (/^\d{3,4}$/.test(text)) {
    const padded = text.padStart(4, "0");
    h = Number(padded.slice(0, 2));
    m = Number(padded.slice(2));
  } else if (/^\d{1,2}$/.test(text)) {
    h = Number(text);
    m = 0;
  } else {
    return null;
  }
  if (h === 24 && m === 0) h = 0;
  if (h < 0 || h > 23 || m < 0 || m > 59) return null;
  return `${String(h).padStart(2, "0")}:${String(m).padStart(2, "0")}`;
}

function minutes(time: string): number | null {
  const canon = canonicalizeTime(time);
  if (!canon) return null;
  return Number(canon.slice(0, 2)) * 60 + Number(canon.slice(3));
}

export type ScheduleIssueKind = "name" | "weekdays" | "time" | "rate" | "overlap" | "count";

export interface ScheduleIssue {
  kind: ScheduleIssueKind;
  /** Index of the period; absent for the count issue. */
  index?: number;
}

const WEEK = 7 * 1440;

/** Intervals a period occupies on the weekly timeline (wrap-around splits at the week's end). */
export function intervals(period: SchedulePeriod): Array<[number, number]> {
  const start = minutes(period.start);
  const end = minutes(period.end);
  if (start === null || end === null || start === end) return [];
  const out: Array<[number, number]> = [];
  for (const day of new Set(period.weekdays)) {
    if (day < 0 || day > 6) continue;
    const from = day * 1440 + start;
    const to = end > start ? day * 1440 + end : (day + 1) * 1440 + end;
    if (to <= WEEK) out.push([from, to]);
    else {
      out.push([from, WEEK]);
      out.push([0, to - WEEK]);
    }
  }
  return out;
}

function overlaps(a: Array<[number, number]>, b: Array<[number, number]>): boolean {
  return a.some(([a0, a1]) => b.some(([b0, b1]) => a0 < b1 && b0 < a1));
}

/** Each problem names its period; an overlap is reported on the later of the two. */
export function validateSchedule(periods: readonly SchedulePeriod[]): ScheduleIssue[] {
  const issues: ScheduleIssue[] = [];
  if (periods.length > MAX_PERIODS) issues.push({ kind: "count" });
  periods.forEach((p, index) => {
    const name = p.name.trim();
    if (!name || [...name].length > 32) issues.push({ kind: "name", index });
    if (p.weekdays.length === 0) issues.push({ kind: "weekdays", index });
    const start = minutes(p.start);
    const end = minutes(p.end);
    if (start === null || end === null || start === end) issues.push({ kind: "time", index });
    if (!Number.isInteger(p.ratePercent) || p.ratePercent < MIN_RATE || p.ratePercent > MAX_RATE) issues.push({ kind: "rate", index });
  });
  const taken = periods.map(intervals);
  for (let i = 1; i < periods.length; i++) {
    for (let j = 0; j < i; j++) {
      if (overlaps(taken[i] as Array<[number, number]>, taken[j] as Array<[number, number]>)) {
        issues.push({ kind: "overlap", index: i });
        break;
      }
    }
  }
  return issues;
}

export function emptyPeriod(): SchedulePeriod {
  return { name: "", weekdays: [1, 2, 3, 4, 5], start: "09:00", end: "18:00", ratePercent: 100 };
}

/** The stored text; empty when there are no periods. */
export function scheduleJson(periods: readonly SchedulePeriod[]): string {
  if (periods.length === 0) return "";
  return JSON.stringify({
    periods: periods.map((p) => ({
      name: p.name.trim(),
      weekdays: [...new Set(p.weekdays)].sort((a, b) => a - b),
      start: canonicalizeTime(p.start) ?? p.start,
      end: canonicalizeTime(p.end) ?? p.end,
      ratePercent: p.ratePercent,
    })),
  });
}

export function parseSchedule(text: string | null | undefined): SchedulePeriod[] {
  if (!text || !text.trim()) return [];
  try {
    const root: unknown = JSON.parse(text);
    const list = typeof root === "object" && root !== null ? (root as { periods?: unknown }).periods : undefined;
    if (!Array.isArray(list)) return [];
    return list.flatMap((raw): SchedulePeriod[] => {
      if (typeof raw !== "object" || raw === null) return [];
      const r = raw as Record<string, unknown>;
      return [
        {
          name: typeof r.name === "string" ? r.name : "",
          weekdays: Array.isArray(r.weekdays) ? r.weekdays.filter((d): d is number => typeof d === "number") : [],
          start: typeof r.start === "string" ? r.start : "",
          end: typeof r.end === "string" ? r.end : "",
          ratePercent: typeof r.ratePercent === "number" ? r.ratePercent : 100,
        },
      ];
    });
  } catch {
    return [];
  }
}
