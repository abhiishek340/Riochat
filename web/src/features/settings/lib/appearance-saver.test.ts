import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { AppearanceSaver } from "./appearance-saver";
import type { FullAppearance } from "./appearance-prefs";

const device: FullAppearance = { preset: "tide", chatFont: "default", chatFontWeight: "regular", fontSize: "standard" };

describe("AppearanceSaver", () => {
  beforeEach(() => vi.useFakeTimers());
  afterEach(() => vi.useRealTimers());

  function make(over: { canSend?: boolean; fail?: boolean } = {}) {
    const send = vi.fn(async (_s: string) => {
      if (over.fail) throw new Error("nope");
    });
    const onError = vi.fn();
    const saver = new AppearanceSaver({ readDevice: () => device, send, canSend: () => over.canSend ?? true, onError });
    return { saver, send, onError };
  }

  it("sends one request 300 ms after the last change, merging the patches", async () => {
    const { saver, send } = make();
    saver.schedule({ preset: "ember" });
    await vi.advanceTimersByTimeAsync(200);
    saver.schedule({ fontSize: "large" });
    await vi.advanceTimersByTimeAsync(299);
    expect(send).not.toHaveBeenCalled();
    await vi.advanceTimersByTimeAsync(1);
    expect(send).toHaveBeenCalledTimes(1);
    expect(JSON.parse(send.mock.calls[0]?.[0] as string)).toEqual({ preset: "ochre", chatFont: "default", chatFontWeight: "regular", fontSize: "large" });
  });
  it("sends nothing without a token", async () => {
    const { saver, send } = make({ canSend: false });
    saver.schedule({ fontSize: "small" });
    await vi.advanceTimersByTimeAsync(400);
    expect(send).not.toHaveBeenCalled();
  });
  it("reports a failed save", async () => {
    const { saver, onError } = make({ fail: true });
    saver.schedule({ fontSize: "small" });
    await vi.advanceTimersByTimeAsync(400);
    expect(onError).toHaveBeenCalledTimes(1);
  });
  it("flush sends pending changes at once", async () => {
    const { saver, send } = make();
    saver.schedule({ chatFont: "mono" });
    expect(saver.hasPending()).toBe(true);
    await saver.flush();
    expect(send).toHaveBeenCalledTimes(1);
    expect(saver.hasPending()).toBe(false);
  });
});
