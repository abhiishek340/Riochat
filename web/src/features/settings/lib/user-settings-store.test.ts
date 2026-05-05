import { describe, expect, it, vi } from "vitest";
import { UserSettingsStore } from "./user-settings-store";

function deferred<T>() {
  let resolve!: (v: T) => void;
  let reject!: (e: unknown) => void;
  const promise = new Promise<T>((res, rej) => {
    resolve = res;
    reject = rej;
  });
  return { promise, resolve, reject };
}

describe("UserSettingsStore", () => {
  it("applies an update optimistically and keeps it on success", async () => {
    const save = deferred<void>();
    const store = new UserSettingsStore({ fetch: async () => ({ "chat.markdown_render": "true" }), save: () => save.promise });
    await store.load();
    const done = store.update({ "chat.markdown_render": "false" });
    expect(store.getSnapshot().values["chat.markdown_render"]).toBe("false");
    save.resolve();
    await done;
    expect(store.getSnapshot().values["chat.markdown_render"]).toBe("false");
  });

  it("rolls back and rethrows when the save fails", async () => {
    const store = new UserSettingsStore({ fetch: async () => ({ a: "1" }), save: async () => Promise.reject(new Error("boom")) });
    await store.load();
    await expect(store.update({ a: "2", fresh: "x" })).rejects.toThrow("boom");
    expect(store.getSnapshot().values).toEqual({ a: "1" });
  });

  it("does not roll back a key a newer edit has taken over", async () => {
    const first = deferred<void>();
    const second = deferred<void>();
    let call = 0;
    const store = new UserSettingsStore({ fetch: async () => ({ a: "1" }), save: () => (++call === 1 ? first.promise : second.promise) });
    await store.load();
    const p1 = store.update({ a: "2" });
    const p2 = store.update({ a: "3" });
    first.reject(new Error("late failure"));
    await expect(p1).rejects.toThrow();
    expect(store.getSnapshot().values.a).toBe("3");
    second.resolve();
    await p2;
    expect(store.getSnapshot().values.a).toBe("3");
  });

  it("a load that finishes during an edit keeps the edited key", async () => {
    const save = deferred<void>();
    const store = new UserSettingsStore({ fetch: async () => ({ a: "server", b: "server" }), save: () => save.promise });
    const pending = store.update({ a: "local" });
    await store.load();
    expect(store.getSnapshot().values).toEqual({ a: "local", b: "server" });
    expect(store.getSnapshot().loaded).toBe(true);
    save.resolve();
    await pending;
  });

  it("notifies subscribers and resets", async () => {
    const store = new UserSettingsStore({ fetch: async () => ({ a: "1" }), save: async () => undefined });
    const listener = vi.fn();
    const off = store.subscribe(listener);
    await store.load();
    expect(listener).toHaveBeenCalledTimes(1);
    store.reset();
    expect(store.getSnapshot()).toEqual({ values: {}, loaded: false, failed: false });
    off();
    store.reset();
    expect(listener).toHaveBeenCalledTimes(2);
  });

  it("flags a failed load, notifies subscribers, and clears the flag when a retry works", async () => {
    let fail = true;
    const store = new UserSettingsStore({
      fetch: async () => {
        if (fail) throw new Error("offline");
        return { a: "1" };
      },
      save: async () => undefined,
    });
    const seen = vi.fn();
    store.subscribe(seen);
    await expect(store.load()).rejects.toThrow("offline");
    expect(store.getSnapshot()).toEqual({ values: {}, loaded: false, failed: true });
    expect(seen).toHaveBeenCalledTimes(1);
    fail = false;
    await store.load();
    expect(store.getSnapshot()).toEqual({ values: { a: "1" }, loaded: true, failed: false });
  });

  it("keeps loaded values and does not flag a failed refresh as a failed load", async () => {
    let fail = false;
    const store = new UserSettingsStore({
      fetch: async () => {
        if (fail) throw new Error("offline");
        return { a: "1" };
      },
      save: async () => undefined,
    });
    await store.load();
    fail = true;
    await expect(store.load()).rejects.toThrow("offline");
    expect(store.getSnapshot().values).toEqual({ a: "1" });
    expect(store.getSnapshot().loaded).toBe(true);
  });
});
