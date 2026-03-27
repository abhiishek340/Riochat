import { describe, expect, it } from "vitest";
import { TextBuffer, ThinkBuffer } from "./buffers";

describe("TextBuffer", () => {
  it("collects deltas and hands them over once", () => {
    const b = new TextBuffer();
    b.push("Hel");
    b.push("lo");
    expect(b.take()).toEqual({ replace: null, append: "Hello" });
    expect(b.hasPending).toBe(false);
  });

  it("a snapshot discards unflushed text, and later deltas continue from it", () => {
    const b = new TextBuffer();
    b.push("abc");
    b.replace("xyz");
    b.push("!");
    expect(b.take()).toEqual({ replace: "xyz", append: "!" });
  });
});

describe("ThinkBuffer", () => {
  it("releases 48 characters per tick", () => {
    const b = new ThinkBuffer();
    b.push("r1", "a".repeat(100));
    expect(b.tick()?.text).toHaveLength(48);
    expect(b.tick()?.text).toHaveLength(48);
    expect(b.tick()?.text).toHaveLength(4);
    expect(b.tick()).toBeNull();
  });

  it("speeds up to 256 characters past a 1024 backlog", () => {
    const b = new ThinkBuffer();
    b.push("r1", "b".repeat(1500));
    expect(b.tick()?.text).toHaveLength(256);
  });

  it("flushes the earlier entry at once when another round starts", () => {
    const b = new ThinkBuffer();
    b.push("r1", "first");
    const out = b.push("r2", "second");
    expect(out).toEqual([{ key: "r1", text: "first" }]);
    expect(b.tick()).toEqual({ key: "r2", text: "second" });
  });

  it("flushAll empties the queue; code points are not split", () => {
    const b = new ThinkBuffer();
    b.push("r", "😀".repeat(30));
    const first = b.tick();
    expect(Array.from(first?.text ?? "")).toHaveLength(30);
    b.push("r", "tail");
    expect(b.flushAll()).toEqual([{ key: "r", text: "tail" }]);
    expect(b.hasPending).toBe(false);
  });
});
