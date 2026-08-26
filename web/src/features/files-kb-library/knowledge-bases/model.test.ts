import { describe, expect, it } from "vitest";
import type { KnowledgeBase } from "../shared/types";
import { kbListQuery, rootsFor } from "./api";
import { headerCheckState, healthOf, isEditable, mergeKbPage, nameMatches, pruneSelection, refreshPages, summaryChanged, toggleCapped } from "./model";

const kb = (over: Partial<KnowledgeBase> = {}): KnowledgeBase => ({
  publicID: "kb1",
  name: "Docs",
  description: "",
  enabled: true,
  scope: "user",
  fileCount: 0,
  readyFileCount: 0,
  processingFileCount: 0,
  revision: 1,
  sortOrder: 0,
  createdAt: "",
  updatedAt: "",
  ...over,
});

describe("healthOf", () => {
  it("describes empty, fully ready, partly ready and not ready bases", () => {
    expect(healthOf(kb())).toEqual({ kind: "none" });
    expect(healthOf(kb({ fileCount: 4, readyFileCount: 4 }))).toEqual({ kind: "all", count: 4 });
    expect(healthOf(kb({ fileCount: 4, readyFileCount: 1 }))).toEqual({ kind: "some", ready: 1, total: 4 });
    expect(healthOf(kb({ fileCount: 3, readyFileCount: 0 }))).toEqual({ kind: "notReady", count: 3 });
  });
});

describe("isEditable / pruneSelection", () => {
  it("lets users edit only personal bases, admins everything (scenario 19)", () => {
    expect(isEditable(kb({ scope: "builtin" }), "user")).toBe(false);
    expect(isEditable(kb({ scope: "user" }), "user")).toBe(true);
    expect(isEditable(kb({ scope: "builtin" }), "admin")).toBe(true);
  });
  it("drops non-selectable rows from a selection", () => {
    const items = [kb({ publicID: "a" }), kb({ publicID: "b", scope: "builtin" })];
    expect([...pruneSelection(new Set(["a", "b", "gone"]), items, "user")]).toEqual(["a"]);
    expect([...pruneSelection(new Set(["a", "b"]), items, "admin")]).toEqual(["a", "b"]);
  });
});

describe("summaryChanged", () => {
  it("notices revision and count changes", () => {
    expect(summaryChanged(kb(), kb())).toBe(false);
    expect(summaryChanged(kb(), kb({ revision: 2 }))).toBe(true);
    expect(summaryChanged(kb(), kb({ readyFileCount: 1 }))).toBe(true);
    expect(summaryChanged(kb(), kb({ processingFileCount: 1 }))).toBe(true);
  });
});

describe("list helpers", () => {
  it("merges pages without duplicates", () => {
    expect(mergeKbPage([kb({ publicID: "a" })], [kb({ publicID: "a" }), kb({ publicID: "b" })]).map((k) => k.publicID)).toEqual(["a", "b"]);
  });
  it("plans refresh pages in chunks of 100", () => {
    expect(refreshPages(0)).toEqual([1]);
    expect(refreshPages(50)).toEqual([1]);
    expect(refreshPages(100)).toEqual([1]);
    expect(refreshPages(101)).toEqual([1, 2]);
    expect(refreshPages(250)).toEqual([1, 2, 3]);
  });
  it("builds queries: sort omitted for default, trimmed search", () => {
    expect(kbListQuery({ page: 1, pageSize: 50, q: " docs ", sort: "default" })).toEqual({ page: 1, page_size: 50, q: "docs" });
    expect(kbListQuery({ page: 2, pageSize: 50, q: "", sort: "files" })).toEqual({ page: 2, page_size: 50, sort: "files" });
  });
  it("routes user writes under /mine and admin writes under /admin", () => {
    expect(rootsFor("user")).toEqual({ read: "/knowledge-bases", write: "/knowledge-bases/mine" });
    expect(rootsFor("admin").write).toBe("/admin/knowledge-bases");
  });
});

describe("capped selection", () => {
  it("blocks the 101st file (scenario 20)", () => {
    let sel = new Set<string>();
    for (let i = 0; i < 100; i += 1) sel = toggleCapped(sel, `f${i}`, 100);
    expect(sel.size).toBe(100);
    expect(toggleCapped(sel, "extra", 100)).toBe(sel);
    expect(toggleCapped(sel, "f3", 100).size).toBe(99);
  });
});

describe("headerCheckState", () => {
  const files = [
    { fileID: "a", canVectorize: true },
    { fileID: "b", canVectorize: true },
    { fileID: "c", canVectorize: false },
  ];
  it("is tri-state over the vectorizable files", () => {
    expect(headerCheckState(new Set(), files)).toBe("none");
    expect(headerCheckState(new Set(["a"]), files)).toBe("some");
    expect(headerCheckState(new Set(["a", "b"]), files)).toBe("all");
  });
});

describe("nameMatches", () => {
  it("ignores case and surrounding spaces", () => {
    expect(nameMatches("Quarterly Report.pdf", " report ")).toBe(true);
    expect(nameMatches("notes.txt", "report")).toBe(false);
    expect(nameMatches("anything", "")).toBe(true);
  });
});
