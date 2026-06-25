// @vitest-environment jsdom
import { cleanup, render } from "@testing-library/react";
import { afterEach, describe, expect, it } from "vitest";
import { MiniMarkdown, parseBlocks, parseInline, safeUrl } from "./mini-markdown";

afterEach(cleanup);

describe("safeUrl", () => {
  it("allows http(s), mailto, anchors and same-origin paths only", () => {
    expect(safeUrl("https://a.example/x")).toBe("https://a.example/x");
    expect(safeUrl("mailto:a@b.c")).toBe("mailto:a@b.c");
    expect(safeUrl("/api/v1/x")).toBe("/api/v1/x");
    expect(safeUrl("#top")).toBe("#top");
    expect(safeUrl("javascript:alert(1)")).toBe("");
    expect(safeUrl("data:text/html,<b>")).toBe("");
    expect(safeUrl("//evil.example")).toBe("");
    expect(safeUrl("/\\evil")).toBe("");
    expect(safeUrl("/\t/evil.example")).toBe("");
    expect(safeUrl("/\n/evil.example")).toBe("");
    expect(safeUrl("https://a.example/\u0000")).toBe("");
  });
});

describe("parseBlocks", () => {
  it("recognises headings, fences, lists, quotes, tables and rules", () => {
    const blocks = parseBlocks("# Title\n\npara line 1\nline 2\n\n```ts\nconst a = 1;\n```\n\n- one\n- two\n\n1. a\n2. b\n\n> quoted\n\n| h1 | h2 |\n| --- | --- |\n| x | y |\n\n---");
    expect(blocks.map((b) => b.type)).toEqual(["heading", "paragraph", "code", "list", "list", "quote", "table", "rule"]);
    expect(blocks[1]).toMatchObject({ text: "para line 1\nline 2" });
    expect(blocks[2]).toMatchObject({ lang: "ts", text: "const a = 1;" });
    expect(blocks[4]).toMatchObject({ ordered: true, items: ["a", "b"] });
    expect(blocks[6]).toMatchObject({ head: ["h1", "h2"], rows: [["x", "y"]] });
  });
  it("lets an unterminated fence run to the end", () => {
    expect(parseBlocks("```\nabc\ndef")[0]).toMatchObject({ type: "code", text: "abc\ndef" });
  });
});

describe("parseInline", () => {
  it("handles code, emphasis, links and images", () => {
    const nodes = parseInline("a `b` **c** *d* [e](https://x.example) ![f](/api/v1/i.png)");
    expect(nodes.map((n) => n.type)).toEqual(["text", "code", "text", "strong", "text", "em", "text", "link", "text", "image"]);
  });
  it("drops unsafe link targets but keeps their text", () => {
    expect(parseInline("[x](javascript:alert(1))")).toEqual([{ type: "text", text: "x" }]);
    expect(parseInline("![x](javascript:alert(1))")).toEqual([{ type: "text", text: "x" }]);
  });
  it("links bare URLs without swallowing trailing punctuation", () => {
    const nodes = parseInline("see https://example.com/a.");
    expect(nodes[1]).toMatchObject({ type: "link", href: "https://example.com/a" });
  });
});

describe("MiniMarkdown", () => {
  it("renders elements, never raw HTML", () => {
    const { container } = render(<MiniMarkdown source={'<script>alert(1)</script> **ok** [l](https://x.example)'} />);
    expect(container.querySelector("script")).toBeNull();
    expect(container.textContent).toContain("<script>alert(1)</script>");
    expect(container.querySelector("strong")?.textContent).toBe("ok");
    const link = container.querySelector("a");
    expect(link?.getAttribute("rel")).toContain("noopener");
    expect(link?.getAttribute("target")).toBe("_blank");
  });
});
