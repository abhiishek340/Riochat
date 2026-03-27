import { describe, expect, it } from "vitest";
import { ReplayDedupe } from "./replay";

function run(snapshot: string, seq: number, after: number, atCursor: string | null, deltas: string[]): string {
  const r = new ReplayDedupe();
  r.begin(snapshot, seq, after, atCursor);
  return snapshot + deltas.map((d) => r.push(d)).join("");
}

describe("ReplayDedupe", () => {
  it("drops replayed deltas the snapshot already has (reload, cursor 0)", () => {
    expect(run("Hello world", 0, 0, "", ["Hel", "lo ", "world"])).toBe("Hello world");
  });

  it("keeps what comes after the snapshot", () => {
    expect(run("Hello wor", 0, 0, "", ["Hel", "lo ", "wor", "ld!"])).toBe("Hello world!");
  });

  it("splits a delta that straddles the end of the snapshot", () => {
    expect(run("Hello w", 0, 0, "", ["Hello wor", "ld"])).toBe("Hello world");
  });

  it("works from a cursor in the middle with the text known there", () => {
    expect(run("Hello world", 5, 5, "Hello", [" wor", "ld", "!"])).toBe("Hello world!");
  });

  it("does not repeat a new word that starts like the answer", () => {
    // the snapshot is "The cat. " and the genuinely new text starts with "The" again
    expect(run("The cat. ", 0, 0, "", ["The ", "cat. ", "The ", "dog."])).toBe("The cat. The dog.");
  });

  it("lets everything through when the snapshot moved the cursor", () => {
    expect(run("Hello", 7, 0, "", [" world"])).toBe("Hello world");
    expect(run("Hello", 7, 0, "", ["Hello"])).toBe("HelloHello");
  });

  it("lets everything through when the text at the cursor is unknown or does not fit", () => {
    expect(run("Hello", 3, 3, null, ["lo"])).toBe("Hellolo");
    expect(run("Hello", 3, 3, "Xyz", ["lo"])).toBe("Hellolo");
  });

  it("appends a delta that does not follow the expected text", () => {
    expect(run("abcdef", 0, 0, "", ["cd", "ef"])).toBe("abcdefcdef");
  });

  it("stop() ends the filtering", () => {
    const r = new ReplayDedupe();
    r.begin("abc", 0, 0, "");
    r.stop();
    expect(r.push("abc")).toBe("abc");
  });
});
