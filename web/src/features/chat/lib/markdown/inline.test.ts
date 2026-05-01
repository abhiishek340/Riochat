import { describe, expect, it } from "vitest";
import { inlineText, type Inline } from "./ast";
import { closeOpenMarkers, parseInline, safeHref } from "./inline";

const t = (value: string): Inline => ({ type: "text", value });
const p = (s: string, streaming = false) => parseInline(s, { streaming });

describe("emphasis", () => {
  it("parses strong, em and combinations", () => {
    expect(p("**bold**")).toEqual([{ type: "strong", children: [t("bold")] }]);
    expect(p("*it*")).toEqual([{ type: "em", children: [t("it")] }]);
    expect(p("_it_")).toEqual([{ type: "em", children: [t("it")] }]);
    expect(p("***both***")).toEqual([{ type: "em", children: [{ type: "strong", children: [t("both")] }] }]);
    expect(p("**a *b* c**")).toEqual([{ type: "strong", children: [t("a "), { type: "em", children: [t("b")] }, t(" c")] }]);
    expect(p("x ~~gone~~ y")).toEqual([t("x "), { type: "del", children: [t("gone")] }, t(" y")]);
  });

  it("leaves arithmetic and snake_case alone", () => {
    expect(p("a * b * c")).toEqual([t("a * b * c")]);
    expect(p("snake_case_name here")).toEqual([t("snake_case_name here")]);
    expect(p("2 ** 3")).toEqual([t("2 ** 3")]);
  });

  it("works next to CJK text and punctuation", () => {
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    expect(p("\u4e2d\u6587**\u52a0\u7c97**\u4e2d\u6587")).toEqual([t("\u4e2d\u6587"), { type: "strong", children: [t("\u52a0\u7c97")] }, t("\u4e2d\u6587")]);
    expect(p("**“\u5f15\u7528”**\u6587\u5b57")).toEqual([{ type: "strong", children: [t("“\u5f15\u7528”")] }, t("\u6587\u5b57")]);
  });

  it("closes unfinished markers while streaming", () => {
    expect(p("**bo", true)).toEqual([{ type: "strong", children: [t("bo")] }]);
    expect(p("some `co", true)).toEqual([t("some "), { type: "code", value: "co" }]);
    expect(p("a *b **c", true)).toEqual([t("a "), { type: "em", children: [t("b "), { type: "strong", children: [t("c")] }] }]);
    expect(p("**bo", false)).toEqual([t("**bo")]);
    expect(closeOpenMarkers("plain")).toBe("plain");
  });
});

describe("code and escapes", () => {
  it("parses code spans, double ticks and unmatched ticks", () => {
    expect(p("`a*b*`")).toEqual([{ type: "code", value: "a*b*" }]);
    expect(p("``a`b``")).toEqual([{ type: "code", value: "a`b" }]);
    expect(p("a ` b")).toEqual([t("a ` b")]);
  });
  it("honours backslash escapes and hard breaks", () => {
    expect(p("\\*not\\*")).toEqual([t("*not*")]);
    expect(p("a  \nb")).toEqual([t("a"), { type: "br" }, t("b")]);
    expect(p("a\nb")).toEqual([t("a\nb")]);
    expect(p("a<br>b")).toEqual([t("a"), { type: "br" }, t("b")]);
  });
});

describe("links and images", () => {
  it("parses inline links with titles and nested emphasis", () => {
    expect(p('[**go**](https://e.example/x "T")')).toEqual([{ type: "link", href: "https://e.example/x", title: "T", children: [{ type: "strong", children: [t("go")] }] }]);
  });
  it("drops unsafe link targets but keeps the text", () => {
    expect(p("[x](javascript:alert(1))")).toEqual([t("x")]);
    expect(safeHref("data:text/html;base64,AA")).toBe("");
    expect(safeHref("//cdn.example/x")).toBe("https://cdn.example/x");
    expect(safeHref("/api/v1/files/f/content")).toBe("/api/v1/files/f/content");
    expect(safeHref("#section")).toBe("#section");
  });
  it("parses images, autolinks and bare urls without trailing punctuation", () => {
    expect(p("![alt](/img.png)")).toEqual([{ type: "image", src: "/img.png", alt: "alt", title: "" }]);
    expect(p("<https://e.example>")[0]).toMatchObject({ type: "link", href: "https://e.example" });
    expect(p("see https://e.example/a/b. Next")).toEqual([t("see "), { type: "link", href: "https://e.example/a/b", title: "", children: [t("https://e.example/a/b")] }, t(". Next")]);
    expect(p("(https://e.example/a_(b))")[1]).toMatchObject({ type: "link", href: "https://e.example/a_(b)" });
  });
  it("handles brackets that are not links", () => {
    expect(p("[1] and [x]")).toEqual([t("[1] and [x]")]);
    expect(inlineText(p("[a](b"))).toBe("[a](b");
  });
});

describe("math and footnotes", () => {
  it("parses inline math but leaves prices alone", () => {
    expect(p("so $x^2$ ok")).toEqual([t("so "), { type: "math", tex: "x^2" }, t(" ok")]);
    expect(p("costs $5 and $6")).toEqual([t("costs $5 and $6")]);
    expect(p("$ x $")).toEqual([t("$ x $")]);
    expect(p("a $$ b")).toEqual([t("a $$ b")]);
  });
  it("numbers footnote references in order of first use", () => {
    const notes: string[] = [];
    const out = parseInline("a[^b] c[^a] d[^b]", { footnotes: notes });
    expect(out.filter((n) => n.type === "footnoteRef")).toEqual([
      { type: "footnoteRef", id: "b", index: 1 },
      { type: "footnoteRef", id: "a", index: 2 },
      { type: "footnoteRef", id: "b", index: 1 },
    ]);
  });
});
