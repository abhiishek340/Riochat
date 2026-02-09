import { describe, expect, it, vi } from "vitest";
import { SessionStore, type ChannelLike } from "./session-store";

function fakeChannel() {
  let handler: ((e: { data: unknown }) => void) | undefined;
  const posted: unknown[] = [];
  const channel: ChannelLike = {
    postMessage: (m) => posted.push(m),
    addEventListener: (_t, l) => { handler = l; },
    close: () => undefined,
  };
  return { channel, posted, receive: (data: unknown) => handler?.({ data }) };
}

describe("SessionStore", () => {
  it("bumps the revision only on real changes", () => {
    const s = new SessionStore({ channel: null, events: null });
    s.set({ accessToken: "a", sessionID: "1" });
    s.set({ accessToken: "a", sessionID: "1" });
    expect(s.revision()).toBe(1);
    s.clear();
    expect(s.revision()).toBe(2);
  });

  it("broadcasts local changes but not silent clears or received snapshots", () => {
    const ch = fakeChannel();
    const s = new SessionStore({ channel: () => ch.channel, events: null });
    s.set({ accessToken: "a", sessionID: "1" });
    s.clear({ silent: true });
    expect(ch.posted).toHaveLength(1);
    ch.receive({ type: "session_snapshot", snapshot: { accessToken: "peer", sessionID: "9" } });
    expect(s.token()).toBe("peer");
    expect(ch.posted).toHaveLength(1);
  });

  it("ignores malformed channel messages", () => {
    const ch = fakeChannel();
    const s = new SessionStore({ channel: () => ch.channel, events: null });
    ch.receive({ type: "session_snapshot", snapshot: { accessToken: 5 } });
    ch.receive("nope");
    ch.receive(null);
    expect(s.token()).toBe("");
  });

  it("notifies subscribers", () => {
    const s = new SessionStore({ channel: null, events: null });
    const fn = vi.fn();
    s.subscribe(fn);
    s.set({ accessToken: "a", sessionID: "1" });
    expect(fn).toHaveBeenCalledTimes(1);
  });
});
