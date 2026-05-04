import { describe, expect, it } from "vitest";
import { coerceText, fixNestedFences, normalizeContent, normalizeMathDelimiters, promoteLineBreaks, replaceArrowsInMath } from "./normalize";

describe("coerceText", () => {
  it("reads loosely typed content", () => {
    expect(coerceText("a")).toBe("a");
    expect(coerceText(null)).toBe("");
    expect(coerceText(12)).toBe("12");
    expect(coerceText(["a", "", "b"])).toBe("a\nb");
    expect(coerceText({ text: "hi" })).toBe("hi");
    expect(coerceText({ other: 1 })).toBe('{\n  "other": 1\n}');
  });
});

describe("normalizeMathDelimiters", () => {
  it("rewrites paren and bracket delimiters outside code", () => {
    expect(normalizeMathDelimiters("see \\(a+b\\) now")).toBe("see $a+b$ now");
    expect(normalizeMathDelimiters("x \\[y\\] z")).toContain("$$\ny\n$$");
    expect(normalizeMathDelimiters("`\\(keep\\)` and \\(go\\)")).toBe("`\\(keep\\)` and $go$");
  });
  it("leaves fenced code alone", () => {
    const fenced = "```\n\\(x\\)\n```";
    expect(normalizeMathDelimiters(fenced)).toBe(fenced);
  });
});

describe("replaceArrowsInMath", () => {
  it("turns arrow characters into commands", () => {
    expect(replaceArrowsInMath("a → b ⇔ c")).toBe("a \\to  b \\Leftrightarrow  c");
  });
});

describe("fixNestedFences", () => {
  it("lengthens the outer fence of a tagged block that contains tagged fences", () => {
    const text = "```markdown\n# t\n```js\ncode\n```\nafter\n```";
    const fixed = fixNestedFences(text);
    expect(fixed.startsWith("````markdown")).toBe(true);
    expect(fixed.trimEnd().endsWith("````")).toBe(true);
  });
  it("keeps the whole nested block together, closing fence included", () => {
    const text = "```markdown\n# t\n```js\ncode\n```\nafter\n```\n\ntail";
    expect(fixNestedFences(text)).toBe("````markdown\n# t\n```js\ncode\n```\nafter\n````\n\ntail");
  });
  it("lengthens the opener while a nested block is still streaming", () => {
    expect(fixNestedFences("```markdown\n```js\ncode\n```\nmore")).toBe("````markdown\n```js\ncode\n```\nmore");
    expect(fixNestedFences("```markdown\n```js\ncode")).toBe("```markdown\n```js\ncode");
  });
  it("leaves ordinary fences alone", () => {
    const text = "```js\na\n```\n\n```py\nb\n```";
    expect(fixNestedFences(text)).toBe(text);
  });
});

describe("promoteLineBreaks", () => {
  it("makes a lone line break a paragraph break but keeps lists and fences", () => {
    expect(promoteLineBreaks("a\nb")).toBe("a\n\nb");
    expect(promoteLineBreaks("- a\n- b")).toBe("- a\n- b");
    expect(promoteLineBreaks("```\na\nb\n```")).toBe("```\na\nb\n```");
  });
});

describe("normalizeContent", () => {
  it("normalises line endings and only promotes breaks for user text", () => {
    expect(normalizeContent("a\r\nb")).toBe("a\nb");
    expect(normalizeContent("a\nb", { variant: "user" })).toBe("a\n\nb");
  });
  it("is stable when applied twice", () => {
    const once = normalizeContent("x \\(y\\)\r\n\r\ntext", { variant: "default" });
    expect(normalizeContent(once, { variant: "default" })).toBe(once);
  });
});
