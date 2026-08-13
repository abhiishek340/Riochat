import { describe, expect, it } from "vitest";
import { fingerprint, parseBlock, readId, readVersion, scanMessageBlocks, sniffComponent, sniffItemCount, widgetKey, widgetTitle } from "./block";

describe("parseBlock", () => {
  it("is incomplete while streaming and invalid when finished", () => {
    expect(parseBlock('{"component":"cha', true)).toMatchObject({ kind: "incomplete", component: "cha" });
    expect(parseBlock('{"component":"cha', false).kind).toBe("invalid");
    expect(parseBlock("", true).kind).toBe("incomplete");
    expect(parseBlock("[1]", false).kind).toBe("invalid");
    expect(parseBlock('{"title":"x"}', false).kind).toBe("invalid");
  });

  it("reads a partial block while streaming without a key", () => {
    const r = parseBlock('{"component":"chart","series":[{"name":"a","data":[1,2', true);
    expect(r.kind).toBe("ok");
    if (r.kind !== "ok") return;
    expect(r.block.complete).toBe(false);
    expect(r.block.key).toBe("");
    expect((r.block.root.props.series as { data: number[] }[])[0]?.data).toEqual([1, 2]);
  });

  it("derives an automatic key from the parsed content, whatever the whitespace", () => {
    const a = parseBlock('{"component":"chart","type":"line"}', false);
    const b = parseBlock('{ "component" : "chart",\n  "type": "line" }', false);
    expect(a.kind === "ok" && b.kind === "ok" && a.block.key === b.block.key).toBe(true);
    expect(a.kind === "ok" && a.block.key.startsWith("auto-")).toBe(true);
  });

  it("reads ids and versions", () => {
    expect(readId("a b")).toBe("");
    expect(readId(" ok_1 ")).toBe("ok_1");
    expect(readId(12)).toBe("12");
    expect(readVersion("v2.1")).toBe(2);
    expect(readVersion(3.9)).toBe(3);
    expect(readVersion(0)).toBe(1);
    expect(readVersion("x")).toBe(1);
    const r = parseBlock('{"component":"c","id":"a b","version":"v2.1"}', false);
    expect(r.kind === "ok" && r.block.id === "" && r.block.version === 2 && r.block.key.startsWith("auto-")).toBe(true);
  });

  it("accepts lenient strings and rejects deep nesting", () => {
    expect(parseBlock('{"component":"c","title":"say "hi" now",}', false).kind).toBe("ok");
    const deep = `{"component":"c","x":${"[".repeat(70)}${"]".repeat(70)}}`;
    expect(parseBlock(deep, false).kind).toBe("invalid");
  });

  it("reads props from the props object or from the remaining fields, and only valid children", () => {
    const flat = parseBlock('{"component":"p","title":"T","children":[{"component":"text","text":"hi"},{"x":1},{"component":""}],"bind":"n","visible":"a > 1"}', false);
    expect(flat.kind === "ok" && flat.block.root.props).toEqual({ title: "T" });
    if (flat.kind !== "ok") return;
    expect(flat.block.root.children).toHaveLength(1);
    expect(flat.block.root.children[0]).toMatchObject({ component: "text", props: { text: "hi" }, closed: true });
    expect(flat.block.root.bind).toBe("n");
    expect(flat.block.root.visible).toBe("a > 1");
    const nested = parseBlock('{"component":"p","props":{"title":"T"},"other":1}', false);
    expect(nested.kind === "ok" && nested.block.root.props).toEqual({ title: "T" });
  });

  it("marks nodes that are still open while streaming", () => {
    const r = parseBlock('{"component":"panel","children":[{"component":"text","text":"a"},{"component":"stat","label":"x"', true);
    if (r.kind !== "ok") throw new Error("expected ok");
    expect(r.block.root.closed).toBe(false);
    expect(r.block.root.children.map((c) => c.closed)).toEqual([true, false]);
  });

  it("sniffs the component and the number of rows begun", () => {
    expect(sniffComponent('{"component":"data-table","col')).toBe("data-table");
    expect(sniffComponent('{"comp')).toBe("");
    expect(sniffItemCount('{"component":"x","items":[{"a":1},{"a":[1,2]},{"b"')).toBe(3);
    expect(sniffItemCount('{"component":"x"')).toBe(0);
  });
});

describe("message scan", () => {
  const fence = (body: string) => "```riochat-ui\n" + body + "\n```";
  it("lists blocks with keys and titles, and reports duplicates", () => {
    const text = [fence('{"component":"stat-grid","id":"s1","title":" Totals "}'), fence('{"component":"chart"}'), fence('{"component":"chart"}')].join("\n\n");
    const scan = scanMessageBlocks(text);
    expect(scan.blocks.map((b) => [b.key.startsWith("auto-") ? "auto" : b.key, b.title])).toEqual([["s1", "Totals"], ["auto", ""], ["auto", ""]]);
    expect(scan.duplicates.size).toBe(1);
  });

  it("skips blocks that do not parse or have no key yet", () => {
    const scan = scanMessageBlocks("```riochat-ui\n{\"component\":\"chart\",\"x\":\n");
    expect(scan.blocks).toHaveLength(0);
    expect(scanMessageBlocks("```riochat-ui\nnot json\n```").blocks).toHaveLength(0);
  });

  it("lists closed widgets with a key from the trimmed source and the title of the page", () => {
    const html = "<title>Orbit &amp; moons</title><div>x</div>";
    const scan = scanMessageBlocks("```riochat-widget\n" + html + "\n```");
    expect(scan.blocks).toEqual([{ key: widgetKey(html), title: "Orbit & moons", kind: "widget" }]);
    expect(widgetKey(`  ${html}\n`)).toBe(widgetKey(html));
    expect(scanMessageBlocks("```riochat-widget\n<p>open").blocks).toHaveLength(0);
  });

  it("falls back to headings and limits titles to 40 code points", () => {
    expect(widgetTitle("<h2>A <b>bold</b> idea</h2>")).toBe("A bold idea");
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    expect(widgetTitle(`<h1>${"\u5b57".repeat(60)}</h1>`)).toBe("\u5b57".repeat(40));
    expect(widgetTitle("<p>none</p>")).toBe("");
  });

  it("fingerprints deterministically", () => {
    expect(fingerprint("abc")).toBe(fingerprint("abc"));
    expect(fingerprint("abc")).not.toBe(fingerprint("abd"));
    expect(fingerprint("")).toBe((0x811c9dc5).toString(36));
  });
});
