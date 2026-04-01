import { describe, expect, it } from "vitest";
import { arrangeTabs, dropIndex, isClosable, isStripRoute, readSnapshot, reorderIds, serverLabel, stripStrings, tabIcon, tabLabel, tabTooltip } from "./strip-model";

const en = stripStrings();

describe("strip model", () => {
  it("reads the shell snapshot and defaults unknown platforms to linux", () => {
    const snap = readSnapshot({
      tabs: [
        { id: "tab-1", server: { mode: "remote", origin: "https://chat.example.com" }, title: "Hi" },
        { id: "tab-2", server: null, title: "" },
      ],
      active: "tab-2",
      platform: "plan9",
    });
    expect(snap?.platform).toBe("linux");
    expect(snap?.active).toBe("tab-2");
    expect(snap?.tabs[1]?.server).toBeNull();
    expect(readSnapshot({ tabs: [{ title: "x" }] })).toBeNull();
    expect(readSnapshot("nope")).toBeNull();
  });

  it("labels tabs by server and keeps the page title for the tooltip", () => {
    const notes = { id: "a", server: null, title: " Notes " };
    expect(tabLabel(notes, en)).toBe("New tab");
    expect(tabTooltip(notes, en)).toBe("Notes \u2014 New tab");
    const remote = { id: "a", server: { mode: "remote" as const, origin: "https://chat.example.com:8443" }, title: "" };
    expect(tabLabel(remote, en)).toBe("chat.example.com:8443");
    expect(tabTooltip(remote, en)).toBe("chat.example.com:8443");
    const local = { id: "a", server: { mode: "local" as const, origin: "http://127.0.0.1:1" }, title: "Chat" };
    expect(tabLabel(local, en)).toBe("This computer");
    expect(tabTooltip(local, en)).toBe("Chat \u2014 This computer");
    expect(serverLabel(null, en)).toBe("");
  });

  it("picks an icon per kind of tab", () => {
    expect(tabIcon({ id: "a", server: null, title: "" })).toBe("plus");
    expect(tabIcon({ id: "a", server: { mode: "local", origin: "x" }, title: "" })).toBe("laptop");
    expect(tabIcon({ id: "a", server: { mode: "remote", origin: "x" }, title: "" })).toBe("globe");
  });

  it("does not offer to close the only, unbound tab", () => {
    const empty = { id: "a", server: null, title: "" };
    expect(isClosable(empty, 1)).toBe(false);
    expect(isClosable(empty, 2)).toBe(true);
    expect(isClosable({ id: "a", server: { mode: "local", origin: "x" }, title: "" }, 1)).toBe(true);
  });

  it("applies a dropped order before the shell confirms it", () => {
    expect(reorderIds(["a", "b", "c"], "c", 0)).toEqual(["c", "a", "b"]);
    expect(reorderIds(["a", "b", "c"], "a", 2)).toEqual(["b", "c", "a"]);
    expect(reorderIds(["a", "b"], "zzz", 0)).toEqual(["a", "b"]);
    const tabs = ["a", "b", "c"].map((id) => ({ id, server: null, title: "" }));
    expect(arrangeTabs(tabs, ["c", "a", "b"]).map((t) => t.id)).toEqual(["c", "a", "b"]);
    expect(arrangeTabs(tabs, null).map((t) => t.id)).toEqual(["a", "b", "c"]);
    expect(arrangeTabs(tabs, ["c"]).map((t) => t.id)).toEqual(["c", "a", "b"]);
  });

  it("provides English labels", () => {
    expect(stripStrings().newTab).toBe("New tab");
  });

  it("drops a dragged tab after every tab whose centre it passed", () => {
    expect(dropIndex([50, 150, 250], 10)).toBe(0);
    expect(dropIndex([50, 150, 250], 160)).toBe(2);
    expect(dropIndex([50, 150, 250], 900)).toBe(3);
    expect(dropIndex([], 5)).toBe(0);
  });

  it("recognises the strip route with or without an extension", () => {
    expect(isStripRoute("/desktop/tabs")).toBe(true);
    expect(isStripRoute("/desktop/tabs/")).toBe(true);
    expect(isStripRoute("/desktop/tabs.html")).toBe(true);
    expect(isStripRoute("/desktop/setup")).toBe(false);
    expect(isStripRoute(null)).toBe(false);
  });
});
