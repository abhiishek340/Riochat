import { describe, expect, it } from "vitest";
import {
  PAGE_SIZE,
  archiveActionFor,
  chunk,
  DEFAULT_FILTERS,
  exportFileName,
  filterWords,
  formatRelative,
  groupByProject,
  hasMorePages,
  isShared,
  listQuery,
  masterState,
  matchesFilters,
  mergePage,
  readConversation,
  readExportManifest,
  relativeParts,
  rowsNeedingChange,
  sortByUpdated,
  type ConversationRow,
} from "./model";

function row(over: Partial<ConversationRow> & { publicID: string }): ConversationRow {
  return {
    title: over.publicID,
    status: "active",
    isStarred: false,
    labels: [],
    projectID: "",
    projectName: "",
    shareStatus: "",
    shareID: "",
    sharedAt: null,
    lastShareAccessedAt: null,
    updatedAt: "2025-01-01T00:00:00Z",
    createdAt: "2025-01-01T00:00:00Z",
    model: "",
    messageCount: 0,
    ...over,
  };
}

describe("readConversation", () => {
  it("parses labels and ignores rows without ids", () => {
    const r = readConversation({ publicID: "c1", labelsJSON: '["a","", 3, "b"]', isStarred: true, status: "archived" });
    expect(r).toMatchObject({ publicID: "c1", labels: ["a", "b"], isStarred: true, status: "archived" });
    expect(readConversation({ title: "x" })).toBeNull();
    expect(readConversation({ publicID: "c", labelsJSON: "{bad" })?.labels).toEqual([]);
  });
});

describe("sharing and filters", () => {
  it("counts a conversation as shared only with an active share and an id", () => {
    expect(isShared({ shareStatus: "active", shareID: "s" })).toBe(true);
    expect(isShared({ shareStatus: "active", shareID: "  " })).toBe(false);
    expect(isShared({ shareStatus: "revoked", shareID: "s" })).toBe(false);
  });
  it("re-applies every structural filter", () => {
    const archived = row({ publicID: "a", status: "archived" });
    expect(matchesFilters(archived, { ...DEFAULT_FILTERS, status: "active" })).toBe(false);
    expect(matchesFilters(archived, { ...DEFAULT_FILTERS, status: "archived" })).toBe(true);
    expect(matchesFilters(row({ publicID: "b", isStarred: true }), { ...DEFAULT_FILTERS, starred: "unstarred" })).toBe(false);
    expect(matchesFilters(row({ publicID: "c", shareStatus: "active", shareID: "s" }), { ...DEFAULT_FILTERS, share: "unshared" })).toBe(false);
    expect(matchesFilters(row({ publicID: "d", projectID: "p1" }), { ...DEFAULT_FILTERS, project: "unassigned" })).toBe(false);
    expect(matchesFilters(row({ publicID: "e", projectID: "p1" }), { ...DEFAULT_FILTERS, project: "p1" })).toBe(true);
    expect(matchesFilters(row({ publicID: "f", projectID: "p2" }), { ...DEFAULT_FILTERS, project: "p1" })).toBe(false);
  });
  it("builds the list query with the page size and an optional search", () => {
    expect(listQuery({ ...DEFAULT_FILTERS, project: "P1" }, 1)).toEqual({ page: 1, page_size: PAGE_SIZE, status: "all", starred: "all", share: "all", project: "P1", q: undefined });
    expect(listQuery({ ...DEFAULT_FILTERS, query: "x" }, 2)).toMatchObject({ page: 2, q: "x" });
  });
});

describe("list merging", () => {
  it("sorts by update time and appends later pages without duplicates", () => {
    const rows = sortByUpdated([row({ publicID: "old", updatedAt: "2025-01-01T00:00:00Z" }), row({ publicID: "new", updatedAt: "2025-02-01T00:00:00Z" })]);
    expect(rows.map((r) => r.publicID)).toEqual(["new", "old"]);
    const merged = mergePage(rows, [row({ publicID: "old", title: "dup" }), row({ publicID: "x" })]);
    expect(merged.map((r) => r.publicID)).toEqual(["new", "old", "x"]);
    expect(merged[1]!.title).toBe("old");
  });
  it("has more pages only after a full page below the reported total", () => {
    expect(hasMorePages(50, 50, 120)).toBe(true);
    expect(hasMorePages(20, 20, 20)).toBe(false);
    expect(hasMorePages(50, 120, 120)).toBe(false);
    expect(hasMorePages(49, 49, 120)).toBe(false);
  });
});

describe("groupByProject", () => {
  it("follows project order, then unknown projects, then the default section", () => {
    const rows = [
      row({ publicID: "u1" }),
      row({ publicID: "b1", projectID: "pb" }),
      row({ publicID: "a1", projectID: "pa" }),
      row({ publicID: "z1", projectID: "gone", projectName: "Ghost" }),
      row({ publicID: "a2", projectID: "pa" }),
    ];
    const sections = groupByProject(rows, [
      { publicID: "pa", name: "Alpha" },
      { publicID: "pb", name: "Beta" },
      { publicID: "pc", name: "Empty" },
    ]);
    expect(sections.map((s) => [s.name, s.rows.map((r) => r.publicID)])).toEqual([
      ["Alpha", ["a1", "a2"]],
      ["Beta", ["b1"]],
      ["Ghost", ["z1"]],
      [null, ["u1"]],
    ]);
  });
});

describe("empty-state wording", () => {
  it("lists the active filters in order", () => {
    expect(filterWords({ ...DEFAULT_FILTERS, status: "archived", share: "shared" })).toEqual(["archived", "shared"]);
    expect(filterWords(DEFAULT_FILTERS)).toEqual([]);
    expect(filterWords({ ...DEFAULT_FILTERS, status: "active", starred: "starred", share: "unshared" })).toEqual(["active", "starred", "unshared"]);
  });
});

describe("bulk planning", () => {
  it("chunks work", () => {
    expect(chunk([1, 2, 3, 4, 5], 2)).toEqual([[1, 2], [3, 4], [5]]);
    expect(chunk([], 3)).toEqual([]);
    expect(chunk(Array.from({ length: 12 }, (_, i) => i), 10).map((c) => c.length)).toEqual([10, 2]);
  });
  it("unarchives only when everything selected is archived", () => {
    const a = row({ publicID: "a", status: "archived" });
    const b = row({ publicID: "b" });
    expect(archiveActionFor([a, a])).toBe("unarchive");
    expect(archiveActionFor([a, b])).toBe("archive");
    expect(archiveActionFor([])).toBe("archive");
    expect(rowsNeedingChange([a, b], "archive").map((r) => r.publicID)).toEqual(["b"]);
    expect(rowsNeedingChange([a, b], "unarchive").map((r) => r.publicID)).toEqual(["a"]);
  });
  it("reports the master checkbox state", () => {
    const rows = [row({ publicID: "a" }), row({ publicID: "b" })];
    expect(masterState(new Set(), rows)).toBe("none");
    expect(masterState(new Set(["a"]), rows)).toBe("some");
    expect(masterState(new Set(["a", "b"]), rows)).toBe("all");
    expect(masterState(new Set(["a"]), [])).toBe("none");
  });
});

describe("export", () => {
  it("reads a manifest line", () => {
    const lines = '{"id":1}\n{"id":2}\n{"_type":"export_manifest","complete":true,"exported":2,"failed":0}\n';
    expect(readExportManifest(lines)).toEqual({ kind: "complete", exported: 2, failed: 0 });
    expect(readExportManifest('{"_type":"export_manifest","complete":false,"exported":5,"failed":2}')).toEqual({ kind: "partial", exported: 5, failed: 2 });
    expect(readExportManifest('{"complete":true,"exported":9,"failed":1}')).toMatchObject({ kind: "partial" });
  });
  it("also reads a manifest that opens the file", () => {
    const lines = '{"_type":"export_manifest","complete":false,"exported":5,"failed":2}\n{"id":1}\n{"id":2}\n';
    expect(readExportManifest(lines)).toEqual({ kind: "partial", exported: 5, failed: 2 });
  });
  it("reports a file without a manifest", () => {
    expect(readExportManifest('{"id":1}\n{"id":2}\n').kind).toBe("downloaded");
    expect(readExportManifest("").kind).toBe("downloaded");
  });
  it("names the file with the UTC date", () => {
    expect(exportFileName(new Date("2025-03-04T23:59:59Z"))).toBe("my-conversations-2025-03-04.jsonl");
  });
});

describe("relative time", () => {
  const now = new Date("2025-06-15T12:00:00Z");
  it("picks the largest sensible unit", () => {
    expect(relativeParts("2025-06-15T11:55:00Z", now)).toEqual({ value: 5, unit: "minute" });
    expect(relativeParts("2025-06-15T09:00:00Z", now)).toEqual({ value: 3, unit: "hour" });
    expect(relativeParts("2025-06-10T12:00:00Z", now)).toEqual({ value: 5, unit: "day" });
    expect(relativeParts("2025-03-15T12:00:00Z", now)).toEqual({ value: 3, unit: "month" });
    expect(relativeParts("2023-06-15T12:00:00Z", now)).toEqual({ value: 2, unit: "year" });
    expect(relativeParts("garbage", now)).toBeNull();
  });
  it("formats in the app language", () => {
    expect(formatRelative("2025-06-15T11:55:00Z", now, "en-US")).toBe("5 minutes ago");
    expect(formatRelative("nope", now, "en-US")).toBeNull();
  });
});
