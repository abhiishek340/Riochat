import { describe, expect, it } from "vitest";
import {
  announcementsSuppressed,
  formatAnnouncementDate,
  formatClock,
  isUnread,
  normalizeType,
  readAnnouncements,
  sortAnnouncements,
  typeRank,
  type Announcement,
} from "./announcements-model";

function item(over: Partial<Announcement> & { id: number }): Announcement {
  return {
    title: `t${over.id}`,
    contentMarkdown: "",
    type: "general",
    pinned: false,
    priority: 0,
    status: "published",
    startsAt: null,
    expiresAt: null,
    createdAt: "2025-01-01T00:00:00Z",
    updatedAt: "2025-01-01T00:00:00Z",
    createdByUserID: 1,
    closedAt: null,
    ...over,
  };
}

describe("types", () => {
  it("treats unknown types as general and ranks the rest", () => {
    expect(normalizeType("bogus")).toBe("general");
    expect(normalizeType("critical")).toBe("critical");
    expect(typeRank("critical")).toBeGreaterThan(typeRank("warning"));
    expect(typeRank("warning")).toBeGreaterThan(typeRank("info"));
    expect(typeRank("normal")).toBeGreaterThan(typeRank("general"));
  });
});

describe("readAnnouncements", () => {
  it("keeps rows with numeric ids and fills defaults", () => {
    const list = readAnnouncements([{ id: 3, title: "A", type: "weird", closedAt: "2025-01-02T00:00:00Z" }, { id: "x" }, null]);
    expect(list).toHaveLength(1);
    expect(list[0]).toMatchObject({ id: 3, title: "A", type: "general", pinned: false });
    expect(isUnread(list[0]!)).toBe(false);
  });
  it("returns nothing for non-arrays", () => {
    expect(readAnnouncements({})).toEqual([]);
  });
});

describe("sortAnnouncements", () => {
  const list = [
    item({ id: 1, type: "info", closedAt: "2025-02-01T00:00:00Z", updatedAt: "2025-03-01T00:00:00Z" }),
    item({ id: 2, type: "critical", updatedAt: "2025-01-05T00:00:00Z" }),
    item({ id: 3, type: "warning", updatedAt: "2025-01-09T00:00:00Z" }),
    item({ id: 4, type: "critical", updatedAt: "2025-01-07T00:00:00Z" }),
  ];
  it("keeps server order by default without mutating", () => {
    expect(sortAnnouncements(list, "default").map((i) => i.id)).toEqual([1, 2, 3, 4]);
  });
  it("puts unread first, then type rank, then newest", () => {
    expect(sortAnnouncements(list, "type").map((i) => i.id)).toEqual([4, 2, 3, 1]);
  });
  it("sorts by time with unread first, ties by id", () => {
    expect(sortAnnouncements(list, "time").map((i) => i.id)).toEqual([3, 4, 2, 1]);
    const ties = [item({ id: 1 }), item({ id: 2 })];
    expect(sortAnnouncements(ties, "time").map((i) => i.id)).toEqual([2, 1]);
  });
});

describe("formatting", () => {
  it("formats dates as yyyy/mm/dd and clocks with padding", () => {
    expect(formatAnnouncementDate("2025-03-04T12:00:00")).toBe("2025/03/04");
    expect(formatAnnouncementDate("nope")).toBe("-");
    expect(formatClock("2025-03-04T01:02:03")).toBe("01:02:03");
    expect(formatClock("nope")).toBe("");
  });
  it("skips share pages", () => {
    expect(announcementsSuppressed("/share")).toBe(true);
    expect(announcementsSuppressed("/share/abc")).toBe(true);
    expect(announcementsSuppressed("/sharing")).toBe(false);
    expect(announcementsSuppressed("/chat")).toBe(false);
  });
});
