// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { SearchItem } from "../lib/search-model";

const searchConversations = vi.hoisted(() => vi.fn());
vi.mock("./search-api", () => ({ searchConversations }));

import { invalidateDefaultSearch, resetSearchCaches, useConversationSearch } from "./use-conversation-search";

const item = (id: string): SearchItem => ({ publicID: id, title: id, projectName: "", status: "active", updatedAt: "2025-01-01T00:00:00Z" });
const page = (ids: string[], hasMore = false) => ({ results: ids.map(item), hasMore });

beforeEach(() => {
  searchConversations.mockReset();
  invalidateDefaultSearch();
});
afterEach(() => vi.useRealTimers());

describe("useConversationSearch", () => {
  it("loads the default list when opened and caches it for a short while", async () => {
    searchConversations.mockResolvedValue(page(["a", "b"]));
    const { result, rerender } = renderHook(({ open }) => useConversationSearch(open), { initialProps: { open: true } });
    await waitFor(() => expect(result.current.items.map((i) => i.publicID)).toEqual(["a", "b"]));
    expect(searchConversations).toHaveBeenCalledWith({ page: 1, query: "" }, expect.anything());
    rerender({ open: false });
    rerender({ open: true });
    await waitFor(() => expect(result.current.items).toHaveLength(2));
    expect(searchConversations).toHaveBeenCalledTimes(1);
  });

  it("shows no leftovers of a typed query after closing without a fresh default list", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    searchConversations.mockResolvedValue(page(["default"]));
    const { result, rerender } = renderHook(({ open }) => useConversationSearch(open), { initialProps: { open: true } });
    await vi.waitFor(() => expect(result.current.items).toHaveLength(1));
    searchConversations.mockResolvedValue(page(["hit"]));
    act(() => result.current.setQuery("x"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    await vi.waitFor(() => expect(result.current.items.map((i) => i.publicID)).toEqual(["hit"]));
    invalidateDefaultSearch();
    rerender({ open: false });
    expect(result.current.items).toEqual([]);
  });

  it("waits 250 ms after typing, then searches with the normalised query", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    searchConversations.mockResolvedValue(page(["default"]));
    const { result } = renderHook(() => useConversationSearch(true));
    await vi.waitFor(() => expect(result.current.items).toHaveLength(1));
    searchConversations.mockClear();
    searchConversations.mockResolvedValue(page(["hit"]));
    act(() => result.current.setQuery("  RePort "));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(200);
    });
    expect(searchConversations).not.toHaveBeenCalled();
    await act(async () => {
      await vi.advanceTimersByTimeAsync(100);
    });
    expect(searchConversations).toHaveBeenCalledWith({ page: 1, query: "report" }, expect.anything());
    await vi.waitFor(() => expect(result.current.items.map((i) => i.publicID)).toEqual(["hit"]));
  });

  it("discards a late response that was superseded by a newer search", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    let resolveSlow: (v: unknown) => void = () => undefined;
    searchConversations.mockImplementation(({ query }: { query: string }) =>
      query === "slow" ? new Promise((resolve) => (resolveSlow = resolve)) : Promise.resolve(page([query || "default"])),
    );
    const { result } = renderHook(() => useConversationSearch(true));
    await vi.waitFor(() => expect(result.current.items.map((i) => i.publicID)).toEqual(["default"]));
    act(() => result.current.setQuery("slow"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    act(() => result.current.setQuery("fast"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    await vi.waitFor(() => expect(result.current.items.map((i) => i.publicID)).toEqual(["fast"]));
    await act(async () => resolveSlow(page(["stale"])));
    expect(result.current.items.map((i) => i.publicID)).toEqual(["fast"]);
  });

  it("appends further pages without duplicates and reports a failed page for retry", async () => {
    searchConversations.mockResolvedValueOnce(page(["a", "b"], true));
    const { result } = renderHook(() => useConversationSearch(true));
    await waitFor(() => expect(result.current.hasMore).toBe(true));
    searchConversations.mockResolvedValueOnce(page(["b", "c"], false));
    await act(async () => result.current.loadMore());
    await waitFor(() => expect(result.current.items.map((i) => i.publicID)).toEqual(["a", "b", "c"]));
    expect(searchConversations).toHaveBeenLastCalledWith({ page: 2, query: "" }, expect.anything());

  });

  it("reports a failed page and lets the caller retry it", async () => {
    searchConversations.mockResolvedValueOnce(page(["a"], true));
    const { result } = renderHook(() => useConversationSearch(true));
    await waitFor(() => expect(result.current.hasMore).toBe(true));
    searchConversations.mockRejectedValueOnce(new Error("down"));
    await act(async () => result.current.loadMore());
    await waitFor(() => expect(result.current.moreError).toBe(true));
    const calls = searchConversations.mock.calls.length;
    await act(async () => result.current.loadMore());
    expect(searchConversations.mock.calls.length).toBe(calls);
    act(() => result.current.retryMore());
    expect(result.current.moreError).toBe(false);
  });

  it("shows an error only when there is nothing to show, and retry invalidates the cache", async () => {
    searchConversations.mockRejectedValueOnce(new Error("down"));
    const { result } = renderHook(() => useConversationSearch(true));
    await waitFor(() => expect(result.current.error).toBe(true));
    searchConversations.mockResolvedValue(page(["ok"]));
    act(() => result.current.retryFirst());
    await waitFor(() => expect(result.current.items.map((i) => i.publicID)).toEqual(["ok"]));
    expect(result.current.error).toBe(false);
  });

  it("does not keep the previous query's results on screen when the new query fails", async () => {
    vi.useFakeTimers({ shouldAdvanceTime: true });
    searchConversations.mockImplementation(({ query }: { query: string }) =>
      query === "" ? Promise.resolve(page(["default"])) : Promise.reject(new Error("down")),
    );
    const { result } = renderHook(() => useConversationSearch(true));
    await vi.waitFor(() => expect(result.current.items).toHaveLength(1));
    act(() => result.current.setQuery("x"));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(300);
    });
    await vi.waitFor(() => expect(result.current.error).toBe(true));
    expect(result.current.items).toEqual([]);
  });

  it("forgets the cached default list on sign-out", async () => {
    searchConversations.mockResolvedValue(page(["mine"]));
    const first = renderHook(() => useConversationSearch(true));
    await waitFor(() => expect(first.result.current.items).toHaveLength(1));
    first.unmount();
    resetSearchCaches();
    searchConversations.mockResolvedValue(page(["theirs"]));
    const second = renderHook(() => useConversationSearch(true));
    await waitFor(() => expect(second.result.current.items.map((i) => i.publicID)).toEqual(["theirs"]));
    expect(searchConversations).toHaveBeenCalledTimes(2);
  });
});
