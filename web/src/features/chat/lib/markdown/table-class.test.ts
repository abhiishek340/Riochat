import { describe, expect, it } from "vitest";
import { classifyColumn, classifyTable, columnWidth, toCsv, toMarkdownTable, visualLength, widen } from "./table-class";

describe("classifyColumn", () => {
  it("recognises numbers, dates, code and compact flags", () => {
    expect(classifyColumn("Revenue", ["$1,200", "3,450.5", "12%", "7"])).toBe("numeric");
    expect(classifyColumn("When", ["2026-01-05", "2026-02-11", "2026/03/01"])).toBe("date");
    expect(classifyColumn("Link", ["https://a.example/x", "https://b.example/y"])).toBe("code");
    expect(classifyColumn("Flag", ["yes", "no", "yes", "no"])).toBe("compact");
    expect(classifyColumn("Hash", ["9fceb02d0ae598e95dc970b74767f19372d61af8", "1111111aaaaaaa"])).toBe("code");
  });

  it("recognises long prose", () => {
    const prose = "This is a fairly long explanation that goes on for a while, covering several points in detail.";
    expect(classifyColumn("Notes", [prose, prose])).toBe("content");
    expect(classifyColumn("Name", ["Alphabet soup", "Beta carotene", "Gamma ray burst"])).toBe("normal");
  });

  it("uses header hints as a nudge, not a rule", () => {
    expect(classifyColumn("price", ["12", "13", "abc", "14"])).toBe("numeric");
    expect(classifyColumn("Name", ["", ""])).toBe("normal");
  });

  it("counts wide characters twice", () => {
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    expect(visualLength("\u4f60\u597d")).toBe(4);
    expect(visualLength("ab")).toBe(2);
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    expect(classifyColumn("\u8bf4\u660e", ["\u8fd9\u662f\u4e00\u4e2a\u76f8\u5f53\u957f\u7684\u8bf4\u660e\u6587\u5b57\uff0c\u7528\u6765\u63cf\u8ff0\u8fd9\u4e00\u884c\u5185\u5bb9\u7684\u5177\u4f53\u7ec6\u8282\u548c\u80cc\u666f\u4fe1\u606f\u3002"])).toBe("content");
  });
});

describe("classifyTable", () => {
  it("waits for three rows while streaming and only widens afterwards", () => {
    const header = ["a", "b"];
    const early = classifyTable(header, [["1", "x"], ["2", "y"]], undefined, true);
    expect(early).toEqual(["normal", "normal"]);
    const later = classifyTable(header, [["1", "x"], ["2", "y"], ["3", "z"]], early, true);
    expect(later[0]).toBe("normal"); // never narrower than before
    expect(widen("compact", "content")).toBe("content");
    expect(widen("content", "compact")).toBe("content");
  });

  it("sizes content columns by how many there are", () => {
    expect(columnWidth("content", 1)).toEqual({ min: 17, max: 30 });
    expect(columnWidth("content", 2).min).toBe(14.5);
    expect(columnWidth("content", 3).min).toBe(12);
    expect(columnWidth("content", 2, true).min).toBe(14);
    expect(columnWidth("numeric", 0).min).toBe(4.5);
  });
});

describe("exports", () => {
  it("writes csv with a BOM and quoting", () => {
    expect(toCsv(["a", "b"], [["1,2", 'say "hi"'], ["x\ny", "z"]])).toBe('﻿a,b\r\n"1,2","say ""hi"""\r\n"x\ny",z');
  });
  it("writes markdown tables with escaped pipes", () => {
    expect(toMarkdownTable(["a|b", "c"], [["1", "2\n3"]])).toBe("| a\\|b | c |\n| --- | --- |\n| 1 | 2 3 |");
  });
});
