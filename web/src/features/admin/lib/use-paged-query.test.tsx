// @vitest-environment jsdom
import { act, renderHook, waitFor } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { usePagedQuery } from "./use-paged-query";

describe("usePagedQuery", () => {
  it("reports a failed request, keeps the old rows and clears the flag on the next success", async () => {
    const fetch = vi.fn();
    fetch.mockResolvedValueOnce({ total: 1, results: ["a"] });
    const onError = vi.fn();
    const { result } = renderHook(() => usePagedQuery<string, { q: string }>({ filters: { q: "" }, fetch, onError }));
    await waitFor(() => expect(result.current.rows).toEqual(["a"]));
    expect(result.current.error).toBe(false);

    fetch.mockRejectedValueOnce(new Error("boom"));
    act(() => result.current.reload());
    await waitFor(() => expect(result.current.error).toBe(true));
    expect(result.current.rows).toEqual(["a"]);
    expect(result.current.loading).toBe(false);
    expect(onError).toHaveBeenCalledTimes(1);

    fetch.mockResolvedValueOnce({ total: 2, results: ["a", "b"] });
    act(() => result.current.reload());
    await waitFor(() => expect(result.current.error).toBe(false));
    await waitFor(() => expect(result.current.rows).toEqual(["a", "b"]));
  });

  it("counts answers, not local edits, so selections survive an optimistic change", async () => {
    const fetch = vi.fn().mockResolvedValue({ total: 2, results: ["a", "b"] });
    const { result } = renderHook(() => usePagedQuery<string, { q: string }>({ filters: { q: "" }, fetch }));
    await waitFor(() => expect(result.current.rows).toEqual(["a", "b"]));
    const first = result.current.loadId;
    expect(first).toBeGreaterThan(0);
    act(() => result.current.setRows((r) => r.map((x) => x.toUpperCase())));
    expect(result.current.rows).toEqual(["A", "B"]);
    expect(result.current.loadId).toBe(first);
    act(() => result.current.reload());
    await waitFor(() => expect(result.current.loadId).toBe(first + 1));
  });

  it("flags a failure on the very first load so the table can offer a retry", async () => {
    const fetch = vi.fn().mockRejectedValue(new Error("down"));
    const { result } = renderHook(() => usePagedQuery<string, { q: string }>({ filters: { q: "" }, fetch }));
    await waitFor(() => expect(result.current.error).toBe(true));
    expect(result.current.loading).toBe(false);
    expect(result.current.rows).toEqual([]);
  });
});
