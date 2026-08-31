import { describe, expect, it } from "vitest";
import { canonicalizeTime, intervals, MAX_PERIODS, parseSchedule, scheduleJson, validateSchedule, type SchedulePeriod } from "./schedule";

const period = (over: Partial<SchedulePeriod> = {}): SchedulePeriod => ({ name: "peak", weekdays: [1], start: "09:00", end: "12:00", ratePercent: 150, ...over });

describe("canonicalizeTime", () => {
  it("pads and normalises", () => {
    expect(canonicalizeTime("830")).toBe("08:30");
    expect(canonicalizeTime("8:00")).toBe("08:00");
    expect(canonicalizeTime("24:00")).toBe("00:00");
    expect(canonicalizeTime("0930")).toBe("09:30");
    expect(canonicalizeTime("7")).toBe("07:00");
  });
  it("accepts full-width digits", () => {
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    expect(canonicalizeTime("\uff10\uff18\uff1a\uff13\uff10")).toBe("08:30");
    expect(canonicalizeTime("\uff12\uff14:\uff10\uff10")).toBe("00:00");
  });
  it("rejects what is not a time", () => {
    for (const bad of ["", "25:00", "12:60", "ab", "24:30", "1:2:3"]) expect(canonicalizeTime(bad), bad).toBeNull();
  });
});

describe("validateSchedule", () => {
  it("accepts a tidy schedule", () => {
    expect(validateSchedule([period(), period({ name: "late", weekdays: [1], start: "12:00", end: "13:00" })])).toEqual([]);
  });
  it("flags each kind of problem on its period", () => {
    expect(validateSchedule([period({ name: " " })])).toEqual([{ kind: "name", index: 0 }]);
    expect(validateSchedule([period({ name: "x".repeat(33) })])).toEqual([{ kind: "name", index: 0 }]);
    expect(validateSchedule([period({ weekdays: [] })])).toEqual([{ kind: "weekdays", index: 0 }]);
    expect(validateSchedule([period({ start: "10:00", end: "10:00" })])).toEqual([{ kind: "time", index: 0 }]);
    expect(validateSchedule([period({ start: "xx" })])).toEqual([{ kind: "time", index: 0 }]);
    expect(validateSchedule([period({ ratePercent: 0 })])).toEqual([{ kind: "rate", index: 0 }]);
    expect(validateSchedule([period({ ratePercent: 10001 })])).toEqual([{ kind: "rate", index: 0 }]);
    expect(validateSchedule([period({ ratePercent: 1.5 })])).toEqual([{ kind: "rate", index: 0 }]);
  });
  it("flags more than 32 periods", () => {
    const many = Array.from({ length: MAX_PERIODS + 1 }, (_, i) => period({ name: `p${i}`, weekdays: [i % 7], start: "01:00", end: "02:00" }));
    expect(validateSchedule(many).some((i) => i.kind === "count")).toBe(true);
  });
  it("flags an overlap on the later period", () => {
    const issues = validateSchedule([period(), period({ name: "b", start: "11:00", end: "13:00" })]);
    expect(issues).toEqual([{ kind: "overlap", index: 1 }]);
  });
  it("lets periods that only touch sit side by side", () => {
    expect(validateSchedule([period({ end: "12:00" }), period({ name: "b", start: "12:00", end: "14:00" })])).toEqual([]);
  });
  it("lets different days share clock times", () => {
    expect(validateSchedule([period(), period({ name: "b", weekdays: [2] })])).toEqual([]);
  });
});

describe("overnight periods", () => {
  it("run past midnight into the next day", () => {
    const monday = period({ weekdays: [1], start: "22:00", end: "06:00" });
    expect(intervals(monday)).toEqual([[1 * 1440 + 22 * 60, 2 * 1440 + 6 * 60]]);
  });
  it("collide with the next morning of another period", () => {
    const issues = validateSchedule([period({ weekdays: [1], start: "22:00", end: "06:00" }), period({ name: "tue", weekdays: [2], start: "05:00", end: "07:00" })]);
    expect(issues).toEqual([{ kind: "overlap", index: 1 }]);
  });
  it("wrap from Saturday night to Sunday morning", () => {
    const sat = period({ weekdays: [6], start: "23:00", end: "02:00" });
    expect(intervals(sat)).toEqual([[6 * 1440 + 23 * 60, 7 * 1440], [0, 120]]);
    const issues = validateSchedule([sat, period({ name: "sun", weekdays: [0], start: "01:00", end: "03:00" })]);
    expect(issues).toEqual([{ kind: "overlap", index: 1 }]);
  });
  it("treats an end of 24:00 as midnight of the next day", () => {
    expect(intervals(period({ weekdays: [1], start: "22:00", end: "24:00" }))).toEqual([[1 * 1440 + 22 * 60, 2 * 1440]]);
  });
});

describe("stored text", () => {
  it("is empty without periods", () => {
    expect(scheduleJson([])).toBe("");
    expect(parseSchedule("")).toEqual([]);
  });
  it("round-trips with canonical times and sorted weekdays", () => {
    const text = scheduleJson([period({ weekdays: [3, 1, 3], start: "8:00" })]);
    expect(JSON.parse(text)).toEqual({ periods: [{ name: "peak", weekdays: [1, 3], start: "08:00", end: "12:00", ratePercent: 150 }] });
    expect(parseSchedule(text)[0]).toMatchObject({ weekdays: [1, 3], start: "08:00" });
  });
  it("ignores text that is not a schedule", () => {
    expect(parseSchedule("nope")).toEqual([]);
    expect(parseSchedule("{}")).toEqual([]);
  });
});
