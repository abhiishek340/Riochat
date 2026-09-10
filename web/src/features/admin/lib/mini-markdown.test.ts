import { describe, expect, it } from "vitest";
import { parseBlocks, parseInline } from "./mini-markdown";

describe("parseInline", () => {
  it("reads code, bold, italic and safe links", () => {
    expect(parseInline("a **b** `c` *d* [e](https://x.test)")).toEqual([
      { t: "text", v: "a " },
      { t: "strong", c: [{ t: "text", v: "b" }] },
      { t: "text", v: " " },
      { t: "code", v: "c" },
      { t: "text", v: " " },
      { t: "em", c: [{ t: "text", v: "d" }] },
      { t: "text", v: " " },
      { t: "link", href: "https://x.test", c: [{ t: "text", v: "e" }] },
    ]);
  });
  it("leaves unsafe link targets as plain text", () => {
    expect(parseInline("[x](javascript:alert(1))").some((n) => n.t === "link")).toBe(false);
  });
  it("keeps lone markers literal", () => {
    expect(parseInline("2 * 3 = 6")).toEqual([{ t: "text", v: "2 * 3 = 6" }]);
  });
});

describe("parseBlocks", () => {
  it("splits headings, lists, quotes, code and paragraphs", () => {
    const blocks = parseBlocks("# Title\n\nline one\nline two\n\n- a\n- b\n\n1. x\n2. y\n\n> quoted\n\n```js\nconst a = 1;\n```\n\n---");
    expect(blocks.map((b) => b.t)).toEqual(["heading", "p", "ul", "ol", "quote", "code", "hr"]);
    expect(blocks[1]).toEqual({ t: "p", c: [{ t: "text", v: "line one line two" }] });
    expect(blocks[5]).toEqual({ t: "code", lang: "js", v: "const a = 1;" });
  });
  it("treats raw HTML as text", () => {
    const [p] = parseBlocks("<script>alert(1)</script>");
    expect(p).toEqual({ t: "p", c: [{ t: "text", v: "<script>alert(1)</script>" }] });
  });
});
