import { describe, expect, it } from "vitest";
import {
  buildBranchIndex,
  defaultSelection,
  loginForShare,
  parseAttachments,
  readSnapshot,
  rewriteSharedFileUrls,
  sharedFileUrl,
  stepSibling,
  visiblePath,
} from "./share-model";

const msg = (id: string, parent: string, role: string, createdAt: string, extra: Record<string, unknown> = {}) => ({
  publicID: id,
  parentPublicID: parent,
  role,
  content: id,
  createdAt,
  ...extra,
});

describe("rewriteSharedFileUrls", () => {
  it("points owner file URLs at the share-scoped endpoint, keeping query and fragment", () => {
    expect(rewriteSharedFileUrls("![x](/api/v1/files/f1/content?v=2)", "abc")).toBe("![x](/api/v1/shared-conversations/abc/files/f1/content?v=2)");
    expect(rewriteSharedFileUrls('<img src="/api/v1/files/f1/content#top">', "abc")).toContain('"/api/v1/shared-conversations/abc/files/f1/content#top"');
  });
  it("requires a lead-in character and encodes ids", () => {
    expect(rewriteSharedFileUrls("see x/api/v1/files/f1/content", "abc")).toBe("see x/api/v1/files/f1/content");
    expect(rewriteSharedFileUrls("a /api/v1/files/f 1/content", "s h")).toBe("a /api/v1/files/f 1/content");
    expect(rewriteSharedFileUrls("(/api/v1/files/a%20b/content)", "s h")).toBe("(/api/v1/shared-conversations/s%20h/files/a%20b/content)");
  });
  it("builds a share file URL", () => {
    expect(sharedFileUrl("a b", "f/1")).toBe("/api/v1/shared-conversations/a%20b/files/f%2F1/content");
  });
});

describe("readSnapshot", () => {
  it("never carries owner request ids and defaults missing values", () => {
    const snap = readSnapshot(
      { shareID: "s1", title: "T", model: "gpt", messages: [{ publicID: "m1", role: "user", runID: "RUN", errorRequestID: "REQ" }], defaultMessagePublicIDs: ["m1", 7] },
      "fallback",
    );
    expect(snap?.shareId).toBe("s1");
    expect(snap?.defaultIds).toEqual(["m1"]);
    const m = snap!.messages[0]!;
    expect(m.status).toBe("success");
    expect(m.modelName).toBe("gpt");
    expect(m.inputTokens).toBe(0);
    expect(m.branchReason).toBe("default");
    expect(JSON.stringify(m)).not.toContain("REQ");
    expect(JSON.stringify(m)).not.toContain("RUN");
  });
  it("falls back to the requested id and rejects non-objects", () => {
    expect(readSnapshot({ messages: [] }, "fb")?.shareId).toBe("fb");
    expect(readSnapshot(null, "fb")).toBeNull();
  });
  it("parses attachments from a JSON string", () => {
    expect(parseAttachments('[{"file_id":"f1","file_name":"a.png","detected_mime":"image/png","kind":"image","file_size":12},{"x":1}]')).toEqual([
      { fileId: "f1", name: "a.png", mime: "image/png", kind: "image", size: 12 },
    ]);
    expect(parseAttachments("not json")).toEqual([]);
    expect(parseAttachments("")).toEqual([]);
  });
});

describe("branches", () => {
  const messages = [
    msg("u1", "", "user", "2025-01-01T00:00:00Z"),
    msg("a1", "u1", "assistant", "2025-01-01T00:00:01Z"),
    msg("a2", "u1", "assistant", "2025-01-01T00:00:02Z", { branchReason: "retry" }),
    msg("u2", "a1", "user", "2025-01-01T00:00:03Z"),
  ].map((m) => m);
  const snap = readSnapshot({ shareID: "s", messages, defaultMessagePublicIDs: ["u1", "a1", "u2"] }, "s")!;
  const index = buildBranchIndex(snap.messages);

  it("follows the default path", () => {
    const path = visiblePath(index, defaultSelection(index, snap.defaultIds));
    expect(path.map((p) => p.message.id)).toEqual(["u1", "a1", "u2"]);
    expect(path[1]!.siblings.map((s) => s.id)).toEqual(["a1", "a2"]);
    expect(path[1]!.position).toBe(0);
  });
  it("shows the newest sibling without an explicit selection", () => {
    const path = visiblePath(index, new Map());
    expect(path.map((p) => p.message.id)).toEqual(["u1", "a2"]);
  });
  it("ignores selections that are no longer valid", () => {
    const path = visiblePath(index, new Map([["u1", "ghost"]]));
    expect(path[1]!.message.id).toBe("a2");
  });
  it("clamps navigation at both ends", () => {
    expect(stepSibling(0, -1, 2)).toBe(0);
    expect(stepSibling(0, 1, 2)).toBe(1);
    expect(stepSibling(1, 1, 2)).toBe(1);
    expect(stepSibling(0, 1, 1)).toBe(0);
  });
  it("treats an unknown parent as a root", () => {
    const orphan = buildBranchIndex(readSnapshot({ messages: [msg("x", "missing", "user", "2025-01-01T00:00:00Z")] }, "s")!.messages);
    expect(visiblePath(orphan, new Map()).map((p) => p.message.id)).toEqual(["x"]);
  });
});

describe("loginForShare", () => {
  it("encodes the share page as the next destination", () => {
    expect(loginForShare("abc")).toBe("/login?next=%2Fshare%3Fconversation_id%3Dabc");
  });
  it("keeps an id with reserved characters inside the next value", () => {
    const next = new URL(loginForShare("a&b#c"), "http://x").searchParams.get("next");
    expect(next).toBe("/share?conversation_id=a%26b%23c");
  });
});
