import { describe, expect, it } from "vitest";
import { highlight, isHighlightable, type Token } from "./highlight";

const types = (tokens: Token[]) => tokens.filter((t) => t.type !== "plain").map((t) => `${t.type}:${t.text}`);
const joined = (tokens: Token[]) => tokens.map((t) => t.text).join("");

describe("highlight", () => {
  it("round-trips the source text for every language", () => {
    const code = 'const a = "x\\"y"; // c\nfunction f(n){ return n*2 }\n/* b */ let s = `t${1}`';
    for (const lang of ["js", "go", "rust", "java", "c", "cs", "php", "py", "rb", "sh", "sql", "yaml", "json", "html", "css", "diff", "swift", "dart"]) {
      expect(joined(highlight(code, lang))).toBe(code);
    }
  });

  it("finds keywords, strings, numbers, comments and calls in javascript", () => {
    expect(types(highlight('const x = foo(42, "hi"); // done', "ts"))).toEqual([
      "keyword:const", "operator:=", "function:foo", "punct:(", "number:42", "punct:,", "string:\"hi\"", "punct:)", "punct:;", "comment:// done",
    ]);
  });

  it("handles python docstrings, decorators and literals", () => {
    const t = types(highlight('@dec\ndef f(x):\n    """doc"""\n    return None  # c', "python"));
    expect(t).toContain("meta:@dec");
    expect(t).toContain("keyword:def");
    expect(t).toContain('string:"""doc"""');
    expect(t).toContain("literal:None");
    expect(t).toContain("comment:# c");
  });

  it("separates json keys from string values", () => {
    expect(types(highlight('{"a": "b", "n": 1.5, "t": true}', "json"))).toEqual([
      "punct:{", 'attr:"a"', "punct::", 'string:"b"', "punct:,", 'attr:"n"', "punct::", "number:1.5", "punct:,", 'attr:"t"', "punct::", "literal:true", "punct:}",
    ]);
  });

  it("marks diff lines", () => {
    expect(highlight("+a\n-b\n@@ x\n c", "diff").map((t) => t.type)).toEqual(["inserted", "deleted", "meta", "plain"]);
  });

  it("highlights html tags and shell variables", () => {
    expect(types(highlight('<a href="x">t</a>', "html"))).toContain("tag:<a");
    expect(types(highlight("echo $HOME --flag", "bash"))).toContain("variable:$HOME");
  });

  it("leaves unknown languages and huge inputs plain", () => {
    expect(highlight("whatever", "klingon")).toEqual([{ type: "plain", text: "whatever" }]);
    expect(isHighlightable("TS")).toBe(true);
    expect(isHighlightable("klingon")).toBe(false);
    expect(highlight("x".repeat(70_000), "js")).toHaveLength(1);
  });

  it("does not hang on unterminated constructs", () => {
    expect(joined(highlight('"never closed\nnext', "js"))).toBe('"never closed\nnext');
    expect(joined(highlight("/* never closed", "js"))).toBe("/* never closed");
    expect(joined(highlight("`never", "js"))).toBe("`never");
  });
});
