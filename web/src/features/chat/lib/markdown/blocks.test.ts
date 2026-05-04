import { describe, expect, it } from "vitest";
import { inlineText, type Block } from "./ast";
import { parseBlocks, slugify } from "./blocks";

const kinds = (blocks: Block[]) => blocks.map((b) => b.type);
const para = (b: Block | undefined) => (b && b.type === "paragraph" ? inlineText(b.children) : "");

describe("basic blocks", () => {
  it("separates paragraphs, headings and rules", () => {
    const blocks = parseBlocks("# Title\n\nFirst para\nstill first\n\n---\n\n## Sub ##\nafter");
    expect(kinds(blocks)).toEqual(["heading", "paragraph", "hr", "heading", "paragraph"]);
    expect(blocks[0]).toMatchObject({ level: 1, id: "title" });
    expect(para(blocks[1])).toBe("First para\nstill first");
    expect(blocks[3]).toMatchObject({ level: 2, text: "Sub" });
  });

  it("makes ids unique and unicode friendly", () => {
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    const blocks = parseBlocks("## \u6982\u8ff0\n\n## \u6982\u8ff0\n\n## Same\n\n## Same");
    expect(blocks.map((b) => (b.type === "heading" ? b.id : ""))).toEqual(["\u6982\u8ff0", "\u6982\u8ff0-1", "same", "same-1"]);
    expect(slugify("  Hello, World!  ")).toBe("hello-world");
  });

  it("treats --- after text as a rule and === as a heading", () => {
    expect(kinds(parseBlocks("text\n---"))).toEqual(["paragraph", "hr"]);
    expect(kinds(parseBlocks("Title\n=====\nbody"))).toEqual(["heading", "paragraph"]);
  });

  it("ends a paragraph at a block start", () => {
    expect(kinds(parseBlocks("a\n> q\nb"))).toEqual(["paragraph", "quote"]);
    expect(kinds(parseBlocks("a\n- x"))).toEqual(["paragraph", "list"]);
    expect(kinds(parseBlocks("a\n```js\nx\n```"))).toEqual(["paragraph", "code"]);
  });
});

describe("code", () => {
  it("reads language, info and body, closed or not", () => {
    const [b] = parseBlocks("```ts {1,2}\nconst a = 1;\n```");
    expect(b).toMatchObject({ type: "code", lang: "ts", closed: true, code: "const a = 1;" });
    const [open] = parseBlocks("```python\nprint(1)", { streaming: true });
    expect(open).toMatchObject({ lang: "python", closed: false, code: "print(1)" });
  });

  it("supports tildes, longer fences and fences inside fences", () => {
    const [b] = parseBlocks("````md\n```js\nx\n```\n````");
    expect(b).toMatchObject({ lang: "md", code: "```js\nx\n```", closed: true });
    expect(parseBlocks("~~~\nraw\n~~~")[0]).toMatchObject({ lang: "", code: "raw" });
  });

  it("parses indented code at the top level", () => {
    expect(parseBlocks("para\n\n    code line\n    more")[1]).toMatchObject({ type: "code", code: "code line\nmore" });
  });
});

describe("lists", () => {
  it("parses nested bullets and ordered lists with a start", () => {
    const [list] = parseBlocks("3. one\n4. two\n   - nested a\n   - nested b\n5. three");
    expect(list).toMatchObject({ type: "list", ordered: true, start: 3 });
    if (list?.type !== "list") throw new Error("expected list");
    expect(list.items).toHaveLength(3);
    expect(kinds(list.items[1]?.children ?? [])).toEqual(["paragraph", "list"]);
  });

  it("detects task items and loose lists", () => {
    const [list] = parseBlocks("- [x] done\n- [ ] todo");
    if (list?.type !== "list") throw new Error("expected list");
    expect(list.items.map((i) => i.checked)).toEqual([true, false]);
    const [loose] = parseBlocks("- a\n\n- b");
    if (loose?.type !== "list") throw new Error("expected list");
    expect(loose.tight).toBe(false);
    const [tight] = parseBlocks("- a\n- b");
    expect(tight).toMatchObject({ tight: true });
  });

  it("keeps a code fence inside an item", () => {
    const [list] = parseBlocks("1. step\n   ```sh\n   ls\n   ```\n2. next");
    if (list?.type !== "list") throw new Error("expected list");
    expect(kinds(list.items[0]?.children ?? [])).toEqual(["paragraph", "code"]);
    expect(list.items).toHaveLength(2);
  });

  it("does not mix bullet kinds in one list", () => {
    expect(kinds(parseBlocks("- a\n* b"))).toEqual(["list", "list"]);
  });
});

describe("quotes, tables, math, footnotes", () => {
  it("nests quotes and supports lazy continuation", () => {
    const [q] = parseBlocks("> a\nlazy\n> > deep");
    if (q?.type !== "quote") throw new Error("expected quote");
    expect(para(q.children[0])).toBe("a\nlazy");
    expect(kinds(q.children)).toContain("quote");
  });

  it("parses tables with alignment and escaped pipes", () => {
    const [t] = parseBlocks("| a | b |\n|:--|--:|\n| 1 \\| 2 | `x|y` |\n| 3 | 4 |");
    if (t?.type !== "table") throw new Error("expected table");
    expect(t.align).toEqual(["left", "right"]);
    expect(t.rows.map((r) => r.map((c) => c.text))).toEqual([["1 | 2", "x|y"], ["3", "4"]]);
    expect(t.complete).toBe(true);
  });

  it("does not treat a pipe line without a delimiter row as a table", () => {
    expect(kinds(parseBlocks("a | b\nnot a delimiter"))).toEqual(["paragraph"]);
  });

  it("flags a table that is still being streamed", () => {
    const [t] = parseBlocks("| a |\n|---|\n| 1 |", { streaming: true });
    expect(t).toMatchObject({ type: "table", complete: false });
  });

  it("parses display math in all shapes", () => {
    expect(parseBlocks("$$x^2$$")[0]).toMatchObject({ type: "math", tex: "x^2", closed: true });
    expect(parseBlocks("$$\na = b\n$$")[0]).toMatchObject({ type: "math", tex: "a = b", closed: true });
    expect(parseBlocks("$$\na = b", { streaming: true })[0]).toMatchObject({ type: "math", closed: false });
  });

  it("collects footnotes at the end", () => {
    const blocks = parseBlocks("Text[^1] more[^b].\n\n[^1]: First note.\n[^b]: Second\n    continued.");
    expect(kinds(blocks)).toEqual(["paragraph", "footnotes"]);
    const fn = blocks[1];
    if (fn?.type !== "footnotes") throw new Error("expected footnotes");
    expect(fn.items.map((i) => [i.id, i.index])).toEqual([["1", 1], ["b", 2]]);
  });

  it("parses empty input", () => {
    expect(parseBlocks("")).toEqual([]);
    expect(parseBlocks("\n\n  \n")).toEqual([]);
  });
});
