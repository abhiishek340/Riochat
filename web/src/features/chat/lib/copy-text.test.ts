import { describe, expect, it } from "vitest";
import { blockAsText, readableMessageText } from "./copy-text";

const block = (json: string) => "```riochat-ui\n" + json + "\n```";

describe("readableMessageText", () => {
  it("leaves text without blocks untouched", () => {
    expect(readableMessageText("plain **text**", () => undefined)).toBe("plain **text**");
  });

  it("replaces a block by its defaults overlaid with the changed state", () => {
    const content = `Before\n${block('{"component":"slider-card","id":"s1","title":"Budget","state":{"amount":10,"unit":"k"}}')}\nAfter`;
    const out = readableMessageText(content, (key) => (key === "s1" ? { amount: 25 } : undefined));
    expect(out).toBe("Before\nBudget\namount: 25\nunit: k\nAfter");
  });

  it("falls back to the component name and serialises objects", () => {
    const content = block('{"component":"picker","id":"p","state":{"picked":["a","b"]}}');
    expect(readableMessageText(content, () => undefined)).toBe('picker\npicked: ["a","b"]');
  });

  it("keeps an unclosed or invalid block as written", () => {
    const open = '```riochat-ui\n{"component":"picker","id":"p"';
    expect(readableMessageText(open, () => undefined)).toBe(open);
    const bad = block("not json");
    expect(readableMessageText(bad, () => undefined)).toBe(bad);
  });

  it("keeps other fences", () => {
    const code = "```js\nconsole.log(1)\n```\nriochat-ui is mentioned here";
    expect(readableMessageText(code, () => undefined)).toBe(code);
  });
});

describe("blockAsText", () => {
  it("prints only the heading for a block without state", () => {
    expect(blockAsText("", "stat-grid", {})).toBe("stat-grid");
  });
});
