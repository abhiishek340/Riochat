import { describe, expect, it } from "vitest";
import type { FileObject, UploadResponse } from "../shared/types";
import { appendPage, mergeUploads, pickSelection, pruneChecked, selectionAfterDelete, toggleFilter } from "./list-model";
import { listQuery } from "./api";

const ids = (...list: string[]) => list.map((fileID) => ({ fileID }));

describe("pickSelection", () => {
  it("prefers the requested file, then the current one, then the first", () => {
    const items = ids("a", "b", "c");
    expect(pickSelection("c", "b", items)).toBe("c");
    expect(pickSelection("zz", "b", items)).toBe("b");
    expect(pickSelection(null, "zz", items)).toBe("a");
    expect(pickSelection(null, null, [])).toBeNull();
  });
});

describe("selectionAfterDelete", () => {
  it("selects the item that took the deleted slot, else the previous, else none", () => {
    expect(selectionAfterDelete(ids("a", "b", "c"), "b", "b")).toBe("c");
    expect(selectionAfterDelete(ids("a", "b", "c"), "c", "c")).toBe("b");
    expect(selectionAfterDelete(ids("a"), "a", "a")).toBeNull();
  });
  it("keeps an unrelated selection", () => {
    expect(selectionAfterDelete(ids("a", "b", "c"), "b", "c")).toBe("c");
  });
});

describe("appendPage / pruneChecked", () => {
  it("drops duplicates by file ID", () => {
    expect(appendPage(ids("a", "b"), ids("b", "c")).map((f) => f.fileID)).toEqual(["a", "b", "c"]);
  });
  it("forgets checked files that are no longer loaded", () => {
    expect([...pruneChecked(new Set(["a", "x"]), ids("a", "b"))]).toEqual(["a"]);
    const same = new Set(["a"]);
    expect(pruneChecked(same, ids("a"))).toBe(same);
  });
});

function upload(fileID: string, fileName: string, mimeType: string, reused = false): UploadResponse {
  return {
    reused,
    quota: { quotaBytes: 100, usedBytes: fileID.length },
    file: { fileID, fileName, mimeType } as FileObject,
  };
}

describe("mergeUploads", () => {
  const base = { items: [{ fileID: "old", fileName: "old.pdf", mimeType: "application/pdf" } as FileObject], total: 1, query: "", filters: [] as string[] };
  it("inserts new files on top, counts them and selects the last one", () => {
    const out = mergeUploads({ ...base, results: [upload("n1", "a.pdf", "application/pdf"), upload("n2", "b.pdf", "application/pdf")] });
    expect(out.items.map((f) => f.fileID)).toEqual(["n2", "n1", "old"]);
    expect(out.total).toBe(3);
    expect(out.selectID).toBe("n2");
    expect(out.quota?.usedBytes).toBe(2);
  });
  it("ignores files already listed", () => {
    const out = mergeUploads({ ...base, results: [upload("old", "old.pdf", "application/pdf", true)] });
    expect(out.items).toHaveLength(1);
    expect(out.total).toBe(1);
    expect(out.selectID).toBeNull();
  });
  it("skips a PNG while the pdf filter is active (scenario 7)", () => {
    const out = mergeUploads({ ...base, filters: ["pdf"], results: [upload("img", "photo.png", "image/png")] });
    expect(out.items).toHaveLength(1);
    expect(out.total).toBe(1);
  });
  it("respects the search text", () => {
    const out = mergeUploads({ ...base, query: "report", results: [upload("r", "Q3 Report.pdf", "application/pdf"), upload("x", "other.pdf", "application/pdf")] });
    expect(out.items.map((f) => f.fileID)).toEqual(["r", "old"]);
  });
});

describe("toggleFilter and listQuery", () => {
  it("toggles keys in selection order", () => {
    expect(toggleFilter(["pdf"], "image")).toEqual(["pdf", "image"]);
    expect(toggleFilter(["pdf", "image"], "pdf")).toEqual(["image"]);
  });
  it("builds the list query like the contract says", () => {
    expect(listQuery({ page: 1, pageSize: 100, q: "  ", kinds: [], sort: "created" })).toEqual({ page: 1, page_size: 100, sort: "created" });
    expect(listQuery({ page: 2, pageSize: 100, q: " report ", kinds: ["pdf", "image"], sort: "name" })).toEqual({
      page: 2,
      page_size: 100,
      sort: "name",
      q: "report",
      kind: "pdf,image",
    });
  });
});
