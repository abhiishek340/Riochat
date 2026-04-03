import { describe, expect, it } from "vitest";
import {
  disableToolPayload,
  enableToolPayload,
  getAtPath,
  isToolPayloadEnabled,
  leafPaths,
  payloadSignature,
  removeAtPath,
  setAtPath,
  stripReserved,
} from "./options";

describe("option paths", () => {
  it("writes and reads dotted paths without mutating the input", () => {
    const base = { a: { b: 1 } };
    const next = setAtPath(base, "a.c.d", 2);
    expect(next).toEqual({ a: { b: 1, c: { d: 2 } } });
    expect(base).toEqual({ a: { b: 1 } });
    expect(getAtPath(next, "a.c.d")).toBe(2);
    expect(getAtPath(next, "a.x.y")).toBeUndefined();
  });

  it("removes a path and empties parents", () => {
    expect(removeAtPath({ a: { b: { c: 1 } }, k: 1 }, "a.b.c")).toEqual({ k: 1 });
    expect(removeAtPath({ a: { b: 1, c: 2 } }, "a.b")).toEqual({ a: { c: 2 } });
    expect(removeAtPath({ a: 1 }, "x.y")).toEqual({ a: 1 });
  });

  it("refuses prototype-polluting paths", () => {
    const out = setAtPath({}, "__proto__.polluted", true);
    expect(({} as Record<string, unknown>).polluted).toBeUndefined();
    expect(out).toEqual({});
  });

  it("lists leaves", () => {
    expect(leafPaths({ a: { b: 1 }, c: "x", d: [1], e: {} })).toEqual([
      { path: "a.b", value: 1 },
      { path: "c", value: "x" },
      { path: "d", value: [1] },
      { path: "e", value: {} },
    ]);
  });

  it("strips reserved keys", () => {
    expect(stripReserved({ prompt: "x", temperature: 1, model: "m" })).toEqual({ temperature: 1 });
  });
});

describe("native tool payloads", () => {
  const payload = { type: "web_search", b: 1, a: 2 };
  it("signature ignores key order", () => {
    expect(payloadSignature({ a: 1, b: 2 })).toBe(payloadSignature({ b: 2, a: 1 }));
  });
  it("enables once and disables every match", () => {
    const on = enableToolPayload({}, payload);
    expect(isToolPayloadEnabled(on, payload)).toBe(true);
    expect(enableToolPayload(on, { a: 2, b: 1, type: "web_search" })).toBe(on);
    const dup = { tools: [payload, { ...payload }, { type: "other" }] };
    expect(disableToolPayload(dup, payload)).toEqual({ tools: [{ type: "other" }] });
    expect(disableToolPayload(on, payload)).toEqual({});
  });
});
