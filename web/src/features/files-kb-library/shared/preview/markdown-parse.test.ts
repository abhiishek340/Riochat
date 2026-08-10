import { describe, expect, it } from "vitest";
import { isSafeHref, parseBlocks } from "./markdown-parse";

describe("parseBlocks", () => {
  it("reads headings, paragraphs, lists, quotes and fences", () => {
    const blocks = parseBlocks("# Title\n\nhello\nworld\n\n- a\n- b\n\n1. x\n2. y\n\n> quoted\n\n```js\nlet a = 1;\n```\n---\n");
    expect(blocks.map((b) => b.type)).toEqual(["heading", "paragraph", "list", "list", "quote", "code", "rule"]);
    expect(blocks[1]).toEqual({ type: "paragraph", text: "hello\nworld" });
    expect(blocks[2]).toEqual({ type: "list", ordered: false, items: ["a", "b"] });
    expect(blocks[3]).toMatchObject({ ordered: true });
    expect(blocks[5]).toEqual({ type: "code", text: "let a = 1;" });
  });
  it("keeps an unterminated fence", () => {
    expect(parseBlocks("```\nabc")).toEqual([{ type: "code", text: "abc" }]);
  });
});

describe("isSafeHref", () => {
  it("allows only web and mail links", () => {
    expect(isSafeHref("https://a.test")).toBe(true);
    expect(isSafeHref("mailto:a@b.c")).toBe(true);
    expect(isSafeHref("javascript:alert(1)")).toBe(false);
  });
});
