import { describe, expect, it } from "vitest";
import { toItem } from "./api";
import {
  charCount,
  checkForm,
  EMPTY_FORM,
  formFromItem,
  LIMITS,
  matchesSearch,
  mergeVisible,
  normalizeName,
  orderItems,
  summaryOf,
  upsert,
  type LibraryItem,
} from "./model";

const item = (over: Partial<LibraryItem> & { id: number }): LibraryItem => ({
  scope: "user",
  title: "t",
  trigger: "t",
  description: "",
  enabled: true,
  sortOrder: 0,
  createdAt: "",
  updatedAt: "",
  ...over,
});

describe("normalizeName", () => {
  it("trims and strips leading slashes", () => {
    expect(normalizeName("  //review ")).toBe("review");
    expect(normalizeName("/ /x")).toBe("/x");
    expect(normalizeName(" / ")).toBe("");
  });
});

describe("checkForm", () => {
  const base = { ...EMPTY_FORM, name: "/review", body: "x" };
  it("accepts a normalised name and trimmed texts (scenario 28)", () => {
    const res = checkForm({ ...base, description: "  d " });
    expect(res).toEqual({ ok: true, value: { name: "review", description: "d", body: "x", enabled: true } });
  });
  it("rejects a missing name or body without further ado (scenario 29)", () => {
    expect(checkForm({ ...base, body: "  " })).toEqual({ ok: false, reason: "missing" });
    expect(checkForm({ ...base, name: " / " })).toEqual({ ok: false, reason: "missing" });
  });
  it("counts characters the way the server does (code points, not UTF-16 units)", () => {
    expect(charCount("a😀")).toBe(2);
    // 64 emoji are 128 UTF-16 units but exactly 64 characters: allowed.
    expect(checkForm({ ...base, name: "😀".repeat(LIMITS.name) }).ok).toBe(true);
    expect(checkForm({ ...base, name: "😀".repeat(LIMITS.name + 1) })).toEqual({ ok: false, reason: "tooLong" });
    // Non-ASCII sample written as escape sequences (CJK / wide characters), so this file stays ASCII-only.
    expect(checkForm({ ...base, body: "\u754c".repeat(LIMITS.body) }).ok).toBe(true);
  });
  it("enforces the length limits", () => {
    expect(checkForm({ ...base, name: "a".repeat(LIMITS.name + 1) })).toEqual({ ok: false, reason: "tooLong" });
    expect(checkForm({ ...base, name: "a".repeat(LIMITS.name) }).ok).toBe(true);
    expect(checkForm({ ...base, description: "d".repeat(LIMITS.description + 1) })).toEqual({ ok: false, reason: "tooLong" });
    expect(checkForm({ ...base, body: "b".repeat(LIMITS.body + 1) })).toEqual({ ok: false, reason: "tooLong" });
  });
});

describe("orderItems", () => {
  it("ranks enabled personal, enabled built-in, then disabled (scenario 34)", () => {
    const items = [
      item({ id: 1, scope: "builtin" }),
      item({ id: 2, enabled: false }),
      item({ id: 3 }),
      item({ id: 4, scope: "builtin", enabled: false }),
    ];
    expect(orderItems("skills", items).map((i) => i.id)).toEqual([3, 1, 4, 2]);
  });
  it("breaks skill ties by sort order, then higher ID", () => {
    const items = [item({ id: 1, sortOrder: 5 }), item({ id: 2, sortOrder: 1 }), item({ id: 3, sortOrder: 1 })];
    expect(orderItems("skills", items).map((i) => i.id)).toEqual([3, 2, 1]);
  });
  it("keeps arrival order for prompts", () => {
    const items = [item({ id: 1, sortOrder: 5 }), item({ id: 2, sortOrder: 1 }), item({ id: 3, sortOrder: 1 })];
    expect(orderItems("prompts", items).map((i) => i.id)).toEqual([1, 2, 3]);
  });
});

describe("matchesSearch", () => {
  it("searches title, trigger and description case-insensitively (scenario 33)", () => {
    const skill = item({ id: 1, title: "Tidy", trigger: "tidy", description: "Refactor code", body: "secret words" });
    expect(matchesSearch("skills", skill, "refactor")).toBe(true);
    expect(matchesSearch("skills", skill, "secret")).toBe(false);
    expect(matchesSearch("prompts", skill, "secret")).toBe(true);
    expect(matchesSearch("skills", skill, "nothing")).toBe(false);
    expect(matchesSearch("skills", skill, "  ")).toBe(true);
  });
});

describe("mergeVisible / upsert", () => {
  it("ignores user-scoped items from the visible endpoint", () => {
    const mine = [item({ id: 1 })];
    const visible = [item({ id: 1 }), item({ id: 9, scope: "builtin" })];
    expect(mergeVisible(mine, visible).map((i) => `${i.scope}${i.id}`)).toEqual(["user1", "builtin9"]);
  });
  it("replaces or prepends", () => {
    const list = [item({ id: 1 }), item({ id: 2 })];
    expect(upsert(list, item({ id: 2, title: "new" }))[1]?.title).toBe("new");
    expect(upsert(list, item({ id: 3 })).map((i) => i.id)).toEqual([3, 1, 2]);
  });
});

describe("display helpers", () => {
  it("prefills the form with trigger, then title", () => {
    expect(formFromItem(item({ id: 1, trigger: "", title: "T", body: "b" })).name).toBe("T");
    expect(formFromItem(item({ id: 1, trigger: "go", title: "T" })).name).toBe("go");
  });
  it("summarises with description, else the first body line", () => {
    expect(summaryOf({ description: "d", body: "x" })).toBe("d");
    expect(summaryOf({ description: " ", body: "\nfirst\nsecond" })).toBe("first");
  });
});

describe("toItem", () => {
  it("reads skills (markdown) and prompts (content)", () => {
    expect(toItem("skills", { id: 3, scope: "builtin", title: "a", trigger: "a", markdown: "# m", enabled: false })).toMatchObject({
      id: 3,
      scope: "builtin",
      body: "# m",
      enabled: false,
    });
    expect(toItem("prompts", { id: 4, content: "hello" })).toMatchObject({ id: 4, scope: "user", body: "hello", enabled: true });
    expect(toItem("skills", { id: 5, title: "no body" })?.body).toBeUndefined();
    expect(toItem("skills", { title: "no id" })).toBeNull();
  });
});
