// @vitest-environment jsdom
import { afterEach, describe, expect, it, vi } from "vitest";
import {
  announceFilesChanged,
  announceKbChanged,
  NonceMemory,
  parseFilesChange,
  parseKbChange,
  subscribeFilesChanged,
  subscribeKbChanged,
} from "./invalidation";

afterEach(() => {
  window.localStorage.clear();
});

describe("parseFilesChange", () => {
  const valid = { reason: "conversation-deleted", deletedCount: 3, nonce: "n1", timestamp: 5, quota: { quotaBytes: 10, usedBytes: 2 } };
  it("accepts a well-formed announcement", () => {
    expect(parseFilesChange(valid)).toMatchObject({ reason: "conversation-deleted", deletedCount: 3, quota: { quotaBytes: 10, usedBytes: 2 } });
  });
  it("ignores malformed payloads", () => {
    for (const bad of [null, "x", {}, { ...valid, reason: "other" }, { ...valid, deletedCount: 0 }, { ...valid, nonce: "" }]) {
      expect(parseFilesChange(bad)).toBeNull();
    }
  });
});

describe("NonceMemory", () => {
  it("accepts each nonce once and forgets the oldest past 50", () => {
    const memory = new NonceMemory();
    expect(memory.take("a")).toBe(true);
    expect(memory.take("a")).toBe(false);
    for (let i = 0; i < 50; i += 1) memory.take(`n${i}`);
    expect(memory.take("a")).toBe(true);
  });
});

describe("files announcements (scenario 16)", () => {
  it("delivers in the same tab once and skips a count of zero", () => {
    const handler = vi.fn();
    const off = subscribeFilesChanged(handler);
    announceFilesChanged({ reason: "project-deleted", deletedCount: 0 });
    expect(handler).not.toHaveBeenCalled();
    announceFilesChanged({ reason: "project-deleted", deletedCount: 3, quota: { quotaBytes: 5, usedBytes: 1 } });
    expect(handler).toHaveBeenCalledTimes(1);
    expect(handler.mock.calls[0]?.[0]).toMatchObject({ deletedCount: 3, quota: { usedBytes: 1 } });
    off();
    announceFilesChanged({ reason: "project-deleted", deletedCount: 1 });
    expect(handler).toHaveBeenCalledTimes(1);
  });
  it("handles a storage event from another tab and drops repeats", () => {
    const handler = vi.fn();
    const off = subscribeFilesChanged(handler);
    const payload = JSON.stringify({ reason: "conversation-deleted", deletedCount: 2, nonce: "x1", timestamp: 1 });
    const fire = () => window.dispatchEvent(new StorageEvent("storage", { key: "riochat:files-changed", newValue: payload }));
    fire();
    fire();
    window.dispatchEvent(new StorageEvent("storage", { key: "riochat:files-changed", newValue: "{not json" }));
    expect(handler).toHaveBeenCalledTimes(1);
    off();
  });
});

describe("knowledge-base announcements", () => {
  it("parses payloads strictly", () => {
    expect(parseKbChange({ nonce: "a", timestamp: 1, knowledgeBaseID: "kb" })).toEqual({ nonce: "a", timestamp: 1, knowledgeBaseID: "kb" });
    expect(parseKbChange({ nonce: "", timestamp: 1 })).toBeNull();
    expect(parseKbChange({ nonce: "a" })).toBeNull();
  });
  it("tells other tabs through storage events", () => {
    const handler = vi.fn();
    const off = subscribeKbChanged(handler);
    announceKbChanged("kb1");
    const stored = window.localStorage.getItem("riochat:knowledge-bases-changed");
    expect(stored).toBeTruthy();
    window.dispatchEvent(new StorageEvent("storage", { key: "riochat:knowledge-bases-changed", newValue: stored }));
    expect(handler).toHaveBeenCalledWith(expect.objectContaining({ knowledgeBaseID: "kb1" }));
    off();
  });
  it("does not throw when storage is unavailable", () => {
    const spy = vi.spyOn(Storage.prototype, "setItem").mockImplementation(() => {
      throw new Error("blocked");
    });
    expect(() => announceKbChanged("kb")).not.toThrow();
    spy.mockRestore();
  });
});
