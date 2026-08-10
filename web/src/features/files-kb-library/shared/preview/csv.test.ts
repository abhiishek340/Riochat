import { describe, expect, it } from "vitest";
import { parseCsv } from "./csv";

describe("parseCsv", () => {
  it("splits rows and cells", () => {
    expect(parseCsv("a,b\n1,2\n").rows).toEqual([["a", "b"], ["1", "2"]]);
  });
  it("handles quotes, embedded commas, doubled quotes and newlines", () => {
    const { rows } = parseCsv('name,note\n"Doe, Jo","said ""hi""\nthen left"\n');
    expect(rows[1]).toEqual(["Doe, Jo", 'said "hi"\nthen left']);
  });
  it("accepts CRLF, a missing final newline and a BOM", () => {
    expect(parseCsv("﻿x,y\r\n1,2").rows).toEqual([["x", "y"], ["1", "2"]]);
  });
  it("skips blank lines and keeps ragged rows", () => {
    expect(parseCsv("a\n\nb,c,d\n").rows).toEqual([["a"], ["b", "c", "d"]]);
  });
  it("stops at the row limit and reports truncation", () => {
    const res = parseCsv("1\n2\n3\n4\n", 2);
    expect(res.rows).toEqual([["1"], ["2"]]);
    expect(res.truncated).toBe(true);
    expect(parseCsv("1\n2\n", 2).truncated).toBe(false);
  });
});
