// @vitest-environment jsdom
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { useLoad } from "./use-load";

afterEach(cleanup);

describe("useLoad", () => {
  it("reports loading in the very render that enables it", async () => {
    const fetcher = vi.fn().mockResolvedValue(["a"]);
    const seen: boolean[] = [];
    const { result, rerender } = renderHook(
      ({ on }: { on: boolean }) => {
        const state = useLoad(fetcher, "k", on);
        seen.push(state.loading);
        return state;
      },
      { initialProps: { on: false } },
    );
    expect(result.current.loading).toBe(false);
    seen.length = 0;
    rerender({ on: true });
    // No render between "enabled" and "request started" may claim to be idle.
    expect(seen[0]).toBe(true);
    await waitFor(() => expect(result.current.data).toEqual(["a"]));
    expect(result.current.loading).toBe(false);
  });

  it("settles with an error and offers a reload that clears it", async () => {
    const fetcher = vi.fn().mockRejectedValueOnce(new Error("down")).mockResolvedValue("ok");
    const { result } = renderHook(() => useLoad(fetcher, "k"));
    await waitFor(() => expect(result.current.error).toBeInstanceOf(Error));
    expect(result.current.loading).toBe(false);
    act(() => result.current.reload());
    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.data).toBe("ok"));
    expect(result.current.error).toBeUndefined();
  });

  it("starts a new load when the key changes", async () => {
    const fetcher = vi.fn().mockImplementation(async () => "x");
    const { result, rerender } = renderHook(({ k }: { k: string }) => useLoad(fetcher, k), { initialProps: { k: "a" } });
    await waitFor(() => expect(result.current.loading).toBe(false));
    rerender({ k: "b" });
    expect(result.current.loading).toBe(true);
    await waitFor(() => expect(result.current.loading).toBe(false));
    expect(fetcher).toHaveBeenCalledTimes(2);
  });
});
