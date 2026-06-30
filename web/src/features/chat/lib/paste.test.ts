// @vitest-environment jsdom
import { describe, expect, it } from "vitest";
import { editorPasteToMarkdown, fenceFor, htmlToMarkdown, insertBlock, isLongPaste } from "./paste";

describe("htmlToMarkdown", () => {
  it("converts inline formatting", () => {
    expect(htmlToMarkdown("<b>x</b>", "x")).toBe("**x**");
    expect(htmlToMarkdown("a <em>b</em> and <del>c</del>", "a b and c")).toBe("a *b* and ~~c~~");
    expect(htmlToMarkdown('<a href="https://e.example">link</a>', "link")).toBe("[link](https://e.example)");
  });

  it("converts headings, lists and code", () => {
    const html = "<h2>Title</h2><ul><li>one</li><li>two<ul><li>deep</li></ul></li></ul><pre><code class=\"language-go\">fmt.Println()</code></pre>";
    const md = htmlToMarkdown(html, "Title\none\ntwo\ndeep\nfmt.Println()");
    expect(md).toContain("## Title");
    expect(md).toContain("- one");
    expect(md).toContain("  - deep");
    expect(md).toContain("```go\nfmt.Println()\n```");
  });

  it("converts tables with pipes escaped", () => {
    const md = htmlToMarkdown("<table><tr><th>a</th><th>b</th></tr><tr><td>1|2</td><td>3</td></tr></table>", "a b\n1|2 3\n");
    expect(md).toBe("| a | b |\n| --- | --- |\n| 1\\|2 | 3 |");
  });

  it("falls back to native paste when nothing is gained or nothing is formatted", () => {
    expect(htmlToMarkdown("<span>plain</span>", "plain")).toBeNull();
    expect(htmlToMarkdown("<p>just text</p>", "just text")).toBeNull();
    expect(htmlToMarkdown("", "x")).toBeNull();
  });

  it("falls back when the conversion equals the plain text or loses line breaks", () => {
    expect(htmlToMarkdown("<a href=''>same</a>", "same")).toBeNull();
    expect(htmlToMarkdown("<b>a</b>", "line1\nline2\nline3\n**a**")).toBeNull();
  });

  it("drops scripts and unsafe links", () => {
    expect(htmlToMarkdown("<b>ok</b><script>alert(1)</script>", "ok")).toBe("**ok**");
    expect(htmlToMarkdown('<a href="javascript:alert(1)"><b>x</b></a>', "x")).toBe("**x**");
  });
});

describe("editor paste", () => {
  it("fences code with the language mode", () => {
    expect(editorPasteToMarkdown('{"version":1,"mode":"typescriptreact"}', "const a = 1;\n")).toBe("```tsx\nconst a = 1;\n```");
    expect(editorPasteToMarkdown('{"version":1,"mode":"shellscript"}', "ls")).toBe("```bash\nls\n```");
  });

  it("uses a longer fence than the code contains", () => {
    expect(editorPasteToMarkdown('{"version":1,"mode":"typescriptreact"}', "x = ```y```")).toBe("````tsx\nx = ```y```\n````");
    expect(fenceFor("no ticks")).toBe("```");
    expect(fenceFor("``` and ````")).toBe("`````");
  });

  it("does not fence markdown, plain text, wrong versions or bad data", () => {
    expect(editorPasteToMarkdown('{"version":1,"mode":"markdown"}', "# t")).toBeNull();
    expect(editorPasteToMarkdown('{"version":1,"mode":"plaintext"}', "t")).toBeNull();
    expect(editorPasteToMarkdown('{"version":2,"mode":"go"}', "t")).toBeNull();
    expect(editorPasteToMarkdown("nope", "t")).toBeNull();
    expect(editorPasteToMarkdown(undefined, "t")).toBeNull();
  });

  it("drops unsafe mode names", () => {
    expect(editorPasteToMarkdown('{"version":1,"mode":"a b\\nc"}', "x")).toBe("```\nx\n```");
  });
});

describe("misc", () => {
  it("treats 2000 UTF-16 units as a long paste", () => {
    expect(isLongPaste("x".repeat(1999))).toBe(false);
    expect(isLongPaste("x".repeat(2000))).toBe(true);
  });

  it("keeps block content on its own lines when inserting", () => {
    expect(insertBlock("hello world", 5, 5, "# H")).toEqual({ text: "hello\n# H world".replace("# H world", "# H\n world"), caret: 9 });
    expect(insertBlock("a", 1, 1, "**b**")).toEqual({ text: "a**b**", caret: 6 });
  });
});
