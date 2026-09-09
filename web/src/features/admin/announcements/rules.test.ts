import { describe, expect, it } from "vitest";
import { bodyOf, dateFromIso, EMPTY_FORM, expiresAtFromDate, isCurrentlyVisible, normalizeType, parsePriorityInput, startsAtFromDate } from "./rules";

describe("isCurrentlyVisible", () => {
  const now = Date.parse("2026-06-15T12:00:00Z");
  it("needs an active status", () => {
    expect(isCurrentlyVisible({ status: "inactive", startsAt: null, expiresAt: null }, now)).toBe(false);
    expect(isCurrentlyVisible({ status: "active", startsAt: null, expiresAt: null }, now)).toBe(true);
  });
  it("is false before the start and after the end", () => {
    expect(isCurrentlyVisible({ status: "active", startsAt: "2026-07-01T00:00:00Z", expiresAt: null }, now)).toBe(false);
    expect(isCurrentlyVisible({ status: "active", startsAt: null, expiresAt: "2026-06-01T00:00:00Z" }, now)).toBe(false);
    expect(isCurrentlyVisible({ status: "active", startsAt: "2026-06-01T00:00:00Z", expiresAt: "2026-07-01T00:00:00Z" }, now)).toBe(true);
  });
});

describe("priority input", () => {
  it("separates blank, invalid and valid text", () => {
    expect(parsePriorityInput("  ")).toEqual({ kind: "blank" });
    expect(parsePriorityInput("abc")).toEqual({ kind: "invalid" });
    expect(parsePriorityInput("1.5")).toEqual({ kind: "invalid" });
    expect(parsePriorityInput("-3")).toEqual({ kind: "ok", value: -3 });
    expect(parsePriorityInput("12")).toEqual({ kind: "ok", value: 12 });
  });
});

describe("dates and form", () => {
  it("maps day pickers to local start and end of day", () => {
    expect(startsAtFromDate("2026-03-02")).toBe(new Date(2026, 2, 2, 0, 0, 0, 0).toISOString());
    expect(expiresAtFromDate("2026-03-02")).toBe(new Date(2026, 2, 2, 23, 59, 59, 0).toISOString());
    expect(startsAtFromDate("")).toBeNull();
    expect(expiresAtFromDate("nonsense")).toBeNull();
  });
  it("normalises unknown types", () => {
    expect(normalizeType("weird")).toBe("general");
    expect(normalizeType("critical")).toBe("critical");
  });
  it("sends nothing when title or content is blank after trimming", () => {
    expect(bodyOf({ ...EMPTY_FORM, title: "  ", content: "x" })).toBeNull();
    expect(bodyOf({ ...EMPTY_FORM, title: "t", content: " \n " })).toBeNull();
  });
  it("trims text and turns non-numeric priority into zero", () => {
    const body = bodyOf({ ...EMPTY_FORM, title: " Hi ", content: " body ", priority: "abc", active: false });
    expect(body).toMatchObject({ title: "Hi", contentMarkdown: "body", priority: 0, status: "inactive", startsAt: null, expiresAt: null });
  });
  it("keeps the stored instants of days the admin did not touch", () => {
    const original = { startsAt: "2026-03-02T09:30:00.000Z", expiresAt: "2026-04-01T17:45:10.000Z" };
    const untouched = bodyOf({ ...EMPTY_FORM, title: "t", content: "c", startDate: dateFromIso(original.startsAt), endDate: dateFromIso(original.expiresAt) }, original);
    expect(untouched).toMatchObject(original);
    const moved = bodyOf({ ...EMPTY_FORM, title: "t", content: "c", startDate: dateFromIso(original.startsAt), endDate: "2030-01-05" }, original);
    expect(moved?.startsAt).toBe(original.startsAt);
    expect(moved?.expiresAt).toBe(new Date(2030, 0, 5, 23, 59, 59, 0).toISOString());
    const cleared = bodyOf({ ...EMPTY_FORM, title: "t", content: "c", startDate: "", endDate: "" }, original);
    expect(cleared).toMatchObject({ startsAt: null, expiresAt: null });
  });
});
