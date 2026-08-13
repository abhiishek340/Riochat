import { describe, expect, it } from "vitest";
import { LenientJsonError, readLenient } from "./lenient-json";

describe("readLenient", () => {
  it("reads a complete document", () => {
    const r = readLenient('{"a":1,"b":[true,null,"x"],"c":{"d":-2.5e1}}');
    expect(r.complete).toBe(true);
    expect(r.value).toEqual({ a: 1, b: [true, null, "x"], c: { d: -25 } });
    expect(r.closed.has(r.value as object)).toBe(true);
  });

  it("tolerates trailing commas, comments, single quotes and unquoted keys", () => {
    const r = readLenient("{ // note\n a: 'one', /* c */ \"b\": [1, 2,], }");
    expect(r.complete).toBe(true);
    expect(r.value).toEqual({ a: "one", b: [1, 2] });
  });

  it("keeps unescaped double quotes inside a string", () => {
    const r = readLenient('{"text": "he said "hi" to me", "n": 1}');
    expect(r.value).toEqual({ text: 'he said "hi" to me', n: 1 });
    expect(r.complete).toBe(true);
  });

  it("reports an unfinished top level and the member that was cut", () => {
    const r = readLenient('{"component":"da');
    expect(r.complete).toBe(false);
    expect(r.value).toEqual({ component: "da" });
    expect(r.cut.get(r.value as object)).toBe("component");
  });

  it("builds nested partial values and tracks which containers closed", () => {
    const r = readLenient('{"items":[{"a":1},{"a":2,"b":');
    const root = r.value as { items: { a?: number }[] };
    expect(r.complete).toBe(false);
    expect(root.items).toHaveLength(2);
    expect(r.closed.has(root.items[0] as object)).toBe(true);
    expect(r.closed.has(root.items[1] as object)).toBe(false);
    expect(r.cut.get(root.items[1] as object)).toBe("b");
    expect(r.cut.get(root.items)).toBe(1);
  });

  it("treats a number touching the end as unfinished", () => {
    const r = readLenient('{"n": 12');
    expect(r.complete).toBe(false);
    expect(r.cut.get(r.value as object)).toBe("n");
    expect(readLenient('{"n": 12}').complete).toBe(true);
  });

  it("treats partial literals as unfinished", () => {
    const r = readLenient('{"a": tr');
    expect(r.complete).toBe(false);
    expect(r.value).toEqual({});
    expect(readLenient('[nul').complete).toBe(false);
  });

  it("reads escapes and cuts an escape at the end", () => {
    expect(readLenient('"a\\nb\\u0041"').value).toBe("a\nbA");
    expect(readLenient('"a\\').value).toBe("a");
    expect(readLenient('"a\\u00').value).toBe("a");
  });

  it("is empty for blank input and throws on garbage", () => {
    expect(readLenient("   ").value).toBeUndefined();
    expect(() => readLenient("{ ?? }")).toThrow(LenientJsonError);
    expect(() => readLenient('{"a": 1 2}')).toThrow(LenientJsonError);
  });

  it("handles a key cut off before its colon", () => {
    const r = readLenient('{"component":"chart","ser');
    expect(r.complete).toBe(false);
    expect(r.cut.get(r.value as object)).toBe("ser");
  });
});
