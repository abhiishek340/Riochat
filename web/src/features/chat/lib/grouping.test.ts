import { describe, expect, it } from "vitest";
import type { ConversationDTO } from "../types";
import { groupConversations, groupOf } from "./grouping";

const conv = (id: string, updatedAt: string, over: Partial<ConversationDTO> = {}): ConversationDTO => ({
  publicID: id, projectID: "", projectName: "", title: id, labelsJSON: "[]", model: "", isStarred: false, starredAt: null, messageCount: 1, status: "active",
  shareStatus: "none", shareID: "", createdAt: updatedAt, updatedAt, ...over,
});

describe("grouping", () => {
  const now = new Date(2026, 5, 15, 14, 0, 0);
  const at = (days: number, hour = 9) => new Date(2026, 5, 15 - days, hour).toISOString();

  it("buckets by calendar day", () => {
    expect(groupOf(conv("a", at(0)), now)).toBe("today");
    expect(groupOf(conv("b", at(1, 23)), now)).toBe("yesterday");
    expect(groupOf(conv("c", at(5)), now)).toBe("previous7");
    expect(groupOf(conv("d", at(20)), now)).toBe("previous30");
    expect(groupOf(conv("e", at(90)), now)).toBe("older");
    expect(groupOf(conv("f", "garbage"), now)).toBe("older");
    expect(groupOf(conv("g", at(90), { isStarred: true }), now)).toBe("starred");
  });

  it("orders groups and omits empty ones, newest first", () => {
    const groups = groupConversations([conv("old", at(90)), conv("t1", at(0, 8)), conv("t2", at(0, 12)), conv("s", at(3), { isStarred: true })], now);
    expect(groups.map((g) => g.id)).toEqual(["starred", "today", "older"]);
    expect(groups[1]?.items.map((c) => c.publicID)).toEqual(["t2", "t1"]);
  });
});
