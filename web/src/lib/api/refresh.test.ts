import { describe, expect, it, vi } from "vitest";
import { ApiError } from "./errors";
import { createRefresher } from "./refresh";
import { SessionStore } from "./session-store";

function setup(transport: () => Promise<{ accessToken: string; sessionID: string } | null>, lock?: <T>(fn: () => Promise<T>) => Promise<T>) {
  const store = new SessionStore({ channel: null, events: null });
  store.set({ accessToken: "old", sessionID: "s1" });
  const refresher = createRefresher({ store, transport, lock });
  return { store, refresher };
}

const unauthorized = () => new ApiError({ message: "x", status: 401 });
const terminated = () => new ApiError({ message: "x", status: 401, errorCode: "auth.invalid_refresh_token" });

describe("refresh state machine", () => {
  it("coalesces concurrent 401s into one refresh and retries each once", async () => {
    const transport = vi.fn(async () => ({ accessToken: "new", sessionID: "s2" }));
    const { refresher } = setup(transport);
    const run = vi.fn(async (token: string) => {
      if (token === "old") throw unauthorized();
      return token;
    });
    const results = await Promise.all([refresher.withAuthRetry(run), refresher.withAuthRetry(run)]);
    expect(results).toEqual(["new", "new"]);
    expect(transport).toHaveBeenCalledTimes(1);
    expect(run).toHaveBeenCalledTimes(4);
  });

  it("clears the session and rethrows the original error when no token comes back", async () => {
    const { store, refresher } = setup(async () => null);
    const original = unauthorized();
    await expect(refresher.withAuthRetry(async () => { throw original; })).rejects.toBe(original);
    expect(store.token()).toBe("");
  });

  it("keeps the session when the transport fails for another reason", async () => {
    const boom = new Error("network");
    const { store, refresher } = setup(async () => { throw boom; });
    await expect(refresher.withAuthRetry(async () => { throw unauthorized(); })).rejects.toBe(boom);
    expect(store.token()).toBe("old");
  });

  it("does not refresh for non-401 errors", async () => {
    const transport = vi.fn(async () => null);
    const { refresher } = setup(transport);
    const err = new ApiError({ message: "x", status: 500 });
    await expect(refresher.withAuthRetry(async () => { throw err; })).rejects.toBe(err);
    expect(transport).not.toHaveBeenCalled();
  });

  it("clears the session when the retry is session-terminating, not when it fails otherwise", async () => {
    const a = setup(async () => ({ accessToken: "new", sessionID: "s2" }));
    await expect(
      a.refresher.withAuthRetry(async (t) => { throw t === "old" ? unauthorized() : terminated(); }),
    ).rejects.toBeInstanceOf(ApiError);
    expect(a.store.token()).toBe("");

    const b = setup(async () => ({ accessToken: "new", sessionID: "s2" }));
    await expect(
      b.refresher.withAuthRetry(async (t) => { throw t === "old" ? unauthorized() : new ApiError({ message: "x", status: 500 }); }),
    ).rejects.toMatchObject({ status: 500 });
    expect(b.store.token()).toBe("new");
  });

  it("adopts a token another tab stored while waiting for the lock", async () => {
    const transport = vi.fn(async () => ({ accessToken: "fresh", sessionID: "s9" }));
    const ref: { store?: SessionStore } = {};
    const lock = async <T,>(fn: () => Promise<T>) => {
      ref.store?.set({ accessToken: "peer", sessionID: "s5" }); // peer rotated before we got the lock
      return fn();
    };
    const made = setup(transport, lock);
    ref.store = made.store;
    expect(await made.refresher.refresh("old")).toBe("peer");
    expect(transport).not.toHaveBeenCalled();
  });

  it("leaves a newer session alone when a refresh that raced with it returns nothing", async () => {
    const ref: { store?: SessionStore } = {};
    const made = setup(async () => {
      ref.store?.set({ accessToken: "signed-in-again", sessionID: "s7" });
      return null;
    });
    ref.store = made.store;
    expect(await made.refresher.refresh("old")).toBe("");
    expect(made.store.token()).toBe("signed-in-again");
  });

  it("never lets an abort trigger a refresh", async () => {
    const transport = vi.fn(async () => null);
    const { refresher } = setup(transport);
    const abort = new DOMException("x", "AbortError");
    await expect(
      refresher.withAuthRetry(async () => { throw abort; }, { isAbort: (e) => e === abort }),
    ).rejects.toBe(abort);
    expect(transport).not.toHaveBeenCalled();
  });
});
