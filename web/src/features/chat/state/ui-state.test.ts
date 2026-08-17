import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { msg } from "../lib/fixtures";

const put = vi.fn(async () => ({ uiState: {} }));
vi.mock("../api", () => ({ putUiState: (...a: unknown[]) => (put as unknown as (...x: unknown[]) => unknown)(...a) }));
vi.mock("./engine-instance", () => ({ setUiStateCollector: () => undefined }));

import { clearUiState, collectUiStates, DEBOUNCE_MS, readBlockState, syncWriteTarget, writeBlockState, type WriteContext } from "./ui-state";

const settled = msg({ id: "A1", role: "assistant", persisted: true, status: "success", uiState: { srv: { n: 1 } } });
const ctx = (m = settled, temporary = false): WriteContext => ({ conversationId: "c1", message: m, temporary });

beforeEach(() => {
  vi.useFakeTimers();
  put.mockClear();
});
afterEach(() => {
  clearUiState();
  vi.useRealTimers();
});

describe("ui state store", () => {
  it("prefers local changes over the server snapshot", () => {
    expect(readBlockState(ctx(), "srv")).toEqual({ n: 1 });
    writeBlockState(ctx(), "srv", { n: 2 });
    expect(readBlockState(ctx(), "srv")).toEqual({ n: 2 });
    writeBlockState(ctx(), "srv", null);
    expect(readBlockState(ctx(), "srv")).toBeUndefined();
  });

  it("debounces writes and merges them per message", async () => {
    writeBlockState(ctx(), "a", { x: 1 });
    writeBlockState(ctx(), "b", { y: 2 });
    expect(put).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(DEBOUNCE_MS + 5);
    expect(put).toHaveBeenCalledTimes(1);
    expect(put).toHaveBeenCalledWith("c1", "A1", { a: { x: 1 }, b: { y: 2 } }, { keepalive: false });
  });

  it("writes null when the state is restored to defaults", async () => {
    writeBlockState(ctx(), "a", {});
    await vi.advanceTimersByTimeAsync(DEBOUNCE_MS + 5);
    expect(put).toHaveBeenCalledWith("c1", "A1", { a: null }, { keepalive: false });
  });

  it("waits while the message streams and writes once it settles", async () => {
    const streaming = msg({ id: "A2", role: "assistant", persisted: true, status: "pending", uiState: {} });
    writeBlockState(ctx(streaming), "a", { x: 1 });
    await vi.advanceTimersByTimeAsync(DEBOUNCE_MS * 3);
    expect(put).not.toHaveBeenCalled();
    syncWriteTarget({ conversationId: "c1", message: { ...streaming, status: "success" }, temporary: false });
    await vi.advanceTimersByTimeAsync(DEBOUNCE_MS + 5);
    expect(put).toHaveBeenCalledTimes(1);
  });

  it("keeps unpersistable ids and temporary chats local", async () => {
    writeBlockState(ctx(), "bad id!", { x: 1 });
    writeBlockState(ctx(settled, true), "t", { x: 1 });
    await vi.advanceTimersByTimeAsync(DEBOUNCE_MS * 3);
    expect(put).not.toHaveBeenCalled();
    expect(readBlockState(ctx(), "bad id!")).toEqual({ x: 1 });
  });

  it("retries on the next change after a failure", async () => {
    put.mockRejectedValueOnce(new Error("offline"));
    writeBlockState(ctx(), "a", { x: 1 });
    await vi.advanceTimersByTimeAsync(DEBOUNCE_MS + 5);
    writeBlockState(ctx(), "a", { x: 2 });
    await vi.advanceTimersByTimeAsync(DEBOUNCE_MS + 5);
    expect(put).toHaveBeenCalledTimes(2);
    expect(put).toHaveBeenLastCalledWith("c1", "A1", { a: { x: 2 } }, { keepalive: false });
  });

  it("collects non-empty states for the next request", () => {
    writeBlockState(ctx(), "a", { x: 1 }, "Title");
    writeBlockState(ctx(), "srv", null);
    const user = msg({ id: "U1", role: "user", uiState: { z: { q: 1 } } });
    expect(collectUiStates([user, settled])).toEqual([{ messagePublicID: "A1", blockID: "a", title: "Title", state: { x: 1 } }]);
  });
});
