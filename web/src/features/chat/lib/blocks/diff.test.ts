import { describe, expect, it } from "vitest";
import { changeStarts, diffLines, foldDiff } from "./diff";
import { arcPath, linePath, niceAxis, rowsToCsv } from "./chart-geometry";

describe("diffLines", () => {
  it("marks additions, deletions and numbers lines", () => {
    const d = diffLines("a\nb\nc", "a\nx\nc\nd");
    expect(d.map((l) => `${l.kind}:${l.text}`)).toEqual(["same:a", "del:b", "add:x", "same:c", "add:d"]);
    expect(d[1]).toMatchObject({ oldNo: 2, newNo: 0 });
    expect(d[2]).toMatchObject({ oldNo: 0, newNo: 2 });
    expect(d[3]).toMatchObject({ oldNo: 3, newNo: 3 });
  });
  it("handles empty sides and line endings", () => {
    expect(diffLines("", "a\r\nb\r\n").map((l) => l.kind)).toEqual(["add", "add"]);
    expect(diffLines("a\n", "")).toHaveLength(1);
    expect(diffLines("", "")).toEqual([]);
  });
});

describe("foldDiff", () => {
  const lines = diffLines(Array.from({ length: 20 }, (_, i) => `l${i}`).join("\n"), Array.from({ length: 20 }, (_, i) => (i === 10 ? "changed" : `l${i}`)).join("\n"));
  it("folds far-away unchanged lines", () => {
    const rows = foldDiff(lines, 2, false);
    expect(rows.filter((r) => r.type === "fold").map((r) => (r.type === "fold" ? r.count : 0))).toEqual([8, 7]);
    expect(changeStarts(rows)).toHaveLength(1);
  });
  it("shows everything when expanded or unchanged", () => {
    expect(foldDiff(lines, 2, true)).toHaveLength(lines.length);
    expect(foldDiff(diffLines("a\nb", "a\nb"), 1, false).every((r) => r.type === "line")).toBe(true);
  });
});

describe("chart geometry", () => {
  it("builds nice axes", () => {
    const a = niceAxis([0, 97]);
    expect(a.min).toBe(0);
    expect(a.max).toBeGreaterThanOrEqual(97);
    expect(niceAxis([5, 5]).max).toBeGreaterThan(5);
    expect(niceAxis([]).ticks).toEqual([0, 1]);
    expect(niceAxis([3, 8], 5, true).min).toBe(0);
  });
  it("breaks paths at gaps", () => {
    expect(linePath([{ x: 0, y: 0 }, null, { x: 1, y: 1 }, { x: 2, y: 2 }])).toBe("M0.00 0.00M1.00 1.00L2.00 2.00");
  });
  it("draws arcs", () => {
    expect(arcPath(10, 10, 5, 0, Math.PI)).toContain("A5 5 0 0 1");
    expect(arcPath(10, 10, 5, 0, Math.PI * 2)).toContain("a5 5");
  });
  it("escapes csv and neutralises formulas", () => {
    expect(rowsToCsv([{ key: "a", label: "A" }, { key: "b", label: "B" }], [{ a: 'x"y', b: "=1+1" }, { a: -3, b: "n,m" }])).toBe('A,B\r\n"x""y",\'=1+1\r\n-3,"n,m"');
  });
});
