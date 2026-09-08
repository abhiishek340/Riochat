import { describe, expect, it } from "vitest";
import { downloadName, extractArtifacts, looksLikeHtml, looksLikeSvg, pickArtifact, replacementFor, detectKind, artifactLabel } from "./artifacts";
import { replaceFences, scanFences } from "./fences";

describe("scanFences", () => {
  it("reads language, body and closing state", () => {
    const fences = scanFences("text\n```html {x}\n<p>a</p>\n```\nmore\n~~~~js\nlet a\n");
    expect(fences).toHaveLength(2);
    expect(fences[0]).toMatchObject({ lang: "html", body: "<p>a</p>", closed: true, startLine: 1, endLine: 3 });
    expect(fences[1]).toMatchObject({ lang: "js", closed: false });
  });
  it("ignores inline code that looks like a fence and honours longer fences", () => {
    expect(scanFences("```a`b")).toHaveLength(0);
    const f = scanFences("````md\n```js\nx\n```\n````");
    expect(f).toHaveLength(1);
    expect(f[0]?.body).toBe("```js\nx\n```");
  });
  it("strips decoration from the language", () => {
    expect(scanFences("```{.python}\nx\n```")[0]?.lang).toBe("python");
    expect(scanFences("```JSX title=a\nx\n```")[0]?.lang).toBe("jsx");
  });
  it("replaces whole fence ranges", () => {
    const text = "a\n```x\n1\n```\nb\n```y\n2\n```\nc";
    expect(replaceFences(text, (f) => (f.lang === "x" ? "[X]" : null))).toBe("a\n[X]\nb\n```y\n2\n```\nc");
    expect(replaceFences("no fences", () => "z")).toBe("no fences");
  });
});

describe("kind detection", () => {
  it("maps languages", () => {
    expect(detectKind("htm", "x")).toBe("html");
    expect(detectKind("scss", "x")).toBe("css");
    expect(detectKind("mjs", "x")).toBe("javascript");
    expect(detectKind("svg+xml", "x")).toBe("svg");
    expect(detectKind("python", "x")).toBeNull();
  });
  it("sniffs neutral languages", () => {
    expect(detectKind("", '<?xml version="1.0"?><!-- c --><svg xmlns="x"></svg>')).toBe("svg");
    expect(detectKind("text", "<!DOCTYPE html><html></html>")).toBe("html");
    expect(detectKind("plain", "<div>hi</div>")).toBe("html");
    expect(detectKind("xml", "<svg></svg>")).toBe("svg");
    expect(detectKind("xml", "<root></root>")).toBeNull();
    expect(detectKind("markdown", "# hi")).toBeNull();
    expect(looksLikeSvg("<svg:svg></svg:svg>")).toBe(true);
    expect(looksLikeHtml("<span>x</span>")).toBe(false);
  });
});

describe("extractArtifacts", () => {
  it("lists closed artifacts with identities from the owning run", () => {
    const text = "Intro\n```html\n<b>a</b>\n```\n```python\nx\n```\n```css\nbody{}\n```";
    const list = extractArtifacts(text, { owner: "run_1", streaming: false });
    expect(list.map((a) => [a.id, a.kind, a.complete])).toEqual([["run_1:0", "html", true], ["run_1:1", "css", true]]);
    expect(artifactLabel("html", 1)).toBe("HTML #1");
    expect(artifactLabel("javascript", 2)).toBe("JS #2");
  });

  it("counts an unclosed fence only while streaming", () => {
    const text = "```html\n<div>";
    expect(extractArtifacts(text, { owner: "r", streaming: false })).toHaveLength(0);
    const live = extractArtifacts(text, { owner: "r", streaming: true });
    expect(live).toHaveLength(1);
    expect(live[0]).toMatchObject({ complete: false, streaming: true });
  });

  it("skips blank code and takes a bare html reply as one artifact", () => {
    expect(extractArtifacts("```html\n   \n```", { owner: "r", streaming: false })).toHaveLength(0);
    const whole = extractArtifacts("<!doctype html><html><body>hi</body></html>", { owner: "r", streaming: false });
    expect(whole).toHaveLength(1);
    expect(whole[0]?.kind).toBe("html");
    expect(extractArtifacts("just words", { owner: "r", streaming: false })).toHaveLength(0);
  });

  it("selects by kind and code", () => {
    const list = extractArtifacts("```html\na\n```\n```html\nb\n```\n```css\nc\n```", { owner: "r", streaming: false });
    expect(pickArtifact(list, "html", "b")).toBe(1);
    expect(pickArtifact(list, "html", "zzz")).toBe(0);
    expect(pickArtifact(list, "svg", "zzz")).toBe(2);
    expect(pickArtifact([], "html", "")).toBe(-1);
  });
});

describe("replacementFor", () => {
  const cand = (id: string, owner: string, code: string, extra: object = {}) => ({ id, owner, index: 0, kind: "html" as const, code, complete: true, streaming: false, ...extra });
  const prev = { id: "local:0", owner: "local", kind: "html" as const, index: 0, code: "<p>a", streaming: true };

  it("matches by id, run, message, related code, then latest while streaming", () => {
    expect(replacementFor(prev, [cand("local:0", "local", "x")])?.id).toBe("local:0");
    expect(replacementFor(prev, [cand("srv:0", "local", "x")])?.id).toBe("srv:0");
    expect(replacementFor({ ...prev, messageId: "m1" }, [cand("srv:0", "srv", "x", { messageId: "m1" })])?.id).toBe("srv:0");
    expect(replacementFor(prev, [cand("srv:0", "srv", "<p>a</p>")])?.id).toBe("srv:0");
    expect(replacementFor(prev, [cand("srv:0", "srv", "zzz"), cand("srv:1", "srv", "yyy")])?.id).toBe("srv:1");
    expect(replacementFor({ ...prev, streaming: false }, [cand("srv:0", "srv", "zzz")])).toBeNull();
  });
});

describe("download names", () => {
  it("names previews", () => {
    expect(downloadName("html")).toBe("artifact-preview.html");
    expect(downloadName("css")).toBe("artifact-css-preview.html");
    expect(downloadName("javascript")).toBe("artifact-js-preview.html");
    expect(downloadName("svg")).toBe("artifact.svg");
  });
});
