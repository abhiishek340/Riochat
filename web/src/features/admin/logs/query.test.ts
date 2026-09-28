import { describe, expect, it } from "vitest";
import { CLEANUP_OF, cleanupBefore, dateRange, distinct, initialTab, positiveInt, searchParams, sortAuthEvents, visibleTabs, withObserved } from "./query";

describe("tab visibility", () => {
  const all = { multiUser: true, billingGating: true, contentModeration: true, superAdmin: true };
  it("shows every tab to a super administrator with every feature on", () => {
    expect(visibleTabs(all)).toHaveLength(8);
  });
  it("drops tabs whose feature is off", () => {
    expect(visibleTabs({ ...all, multiUser: false })).not.toContain("auth");
    expect(visibleTabs({ ...all, billingGating: false })).not.toContain("orders");
    expect(visibleTabs({ ...all, billingGating: false })).not.toContain("redemptions");
  });
  it("shows moderation only to super administrators", () => {
    expect(visibleTabs({ ...all, superAdmin: false })).not.toContain("moderation");
    expect(visibleTabs({ ...all, contentModeration: false })).not.toContain("moderation");
  });
  it("opens the requested tab only when it is visible", () => {
    const tabs = visibleTabs({ ...all, multiUser: false });
    expect(initialTab("usage", tabs)).toBe("usage");
    expect(initialTab("auth", tabs)).toBe("audit");
    expect(initialTab(null, tabs)).toBe("audit");
  });
  it("never purges redemption records", () => {
    expect(CLEANUP_OF.redemptions).toBeNull();
    expect(CLEANUP_OF.orders).toBe("orders");
  });
});

describe("search", () => {
  it("treats digits as a user id and anything else as text", () => {
    expect(searchParams("42", "user_id")).toEqual({ user_id: "42" });
    expect(searchParams("alice", "user_id")).toEqual({ query: "alice" });
    expect(searchParams("  ", "user_id")).toEqual({});
  });
  it("can route text elsewhere or drop it", () => {
    expect(searchParams("login", "user_id", "event_type")).toEqual({ event_type: "login" });
    expect(searchParams("login", "user_id", null)).toEqual({});
  });
  it("reads a positive code id", () => {
    expect(positiveInt("7")).toBe(7);
    expect(positiveInt("0")).toBeUndefined();
    expect(positiveInt("-1")).toBeUndefined();
    expect(positiveInt("x")).toBeUndefined();
    expect(positiveInt(null)).toBeUndefined();
  });
});

describe("dates", () => {
  it("spans the chosen local days", () => {
    const r = dateRange("2026-03-01", "2026-03-02");
    expect(r.created_from).toBe(new Date(2026, 2, 1, 0, 0, 0, 0).toISOString());
    expect(r.created_to).toBe(new Date(2026, 2, 2, 23, 59, 59, 999).toISOString());
    expect(dateRange("", "")).toEqual({});
  });
  it("builds a cleanup cutoff at local midnight and refuses the future", () => {
    const now = new Date(2026, 5, 15, 12, 0, 0);
    expect(cleanupBefore("2026-06-10", now)).toBe(new Date(2026, 5, 10, 0, 0, 0, 0).toISOString());
    expect(cleanupBefore("2026-06-15", now)).toBe(new Date(2026, 5, 15, 0, 0, 0, 0).toISOString());
    expect(cleanupBefore("2026-06-16", now)).toBeNull();
    expect(cleanupBefore("", now)).toBeNull();
    expect(cleanupBefore("2026-02-30", now)).toBeNull();
  });
});

describe("options and sorting", () => {
  it("lists distinct values alphabetically", () => {
    expect(distinct(["b", "a", "b", "", null, " c "])).toEqual(["a", "b", "c"]);
  });
  it("appends observed values after the known ones", () => {
    expect(withObserved(["a", "b"], ["b", "z"])).toEqual(["a", "b", "z"]);
  });
  it("sorts security events client side", () => {
    const rows = [
      { id: 1, occurredAt: "2026-01-02T00:00:00Z" },
      { id: 2, occurredAt: "2026-01-01T00:00:00Z" },
      { id: 3, occurredAt: "2026-01-03T00:00:00Z" },
    ];
    expect(sortAuthEvents(rows, "occurred_desc").map((r) => r.id)).toEqual([3, 1, 2]);
    expect(sortAuthEvents(rows, "occurred_asc").map((r) => r.id)).toEqual([2, 1, 3]);
    expect(sortAuthEvents(rows, "id_desc").map((r) => r.id)).toEqual([3, 2, 1]);
    expect(sortAuthEvents(rows, "id_asc").map((r) => r.id)).toEqual([1, 2, 3]);
  });
});
