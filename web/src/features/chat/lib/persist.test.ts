import { describe, expect, it } from "vitest";
import type { ChatAttachment, SkillSummaryDTO } from "../types";
import {
  appendToDraft, loadDraft, loadSelection, MAX_AGE_MS, MAX_ENTRIES, memoryIO, moveSelection, readRecents, recordRecent, saveDraft, saveSelection,
} from "./persist";

const att = (id: string, preview?: string): ChatAttachment => ({
  fileId: id, fileName: `${id}.png`, mimeType: "image/png", detectedMime: "image/png", fileCategory: "image", fileSize: 3, durationSeconds: 0, kind: "image",
  processingStatus: "ready", processingReady: true, processingErrorCode: "", processingErrorMessage: "", previewUrl: preview,
});

describe("drafts", () => {
  it("round-trips text and attachment metadata without preview urls", () => {
    const io = memoryIO();
    saveDraft(io, "u1", "c1", { text: "hi", attachments: [att("f1", "blob:x")] }, 1000);
    const draft = loadDraft(io, "u1", "c1", 2000);
    expect(draft?.text).toBe("hi");
    expect(draft?.attachments[0]?.fileId).toBe("f1");
    expect(draft?.attachments[0]?.previewUrl).toBeUndefined();
    expect(JSON.stringify([...io.data.values()])).not.toContain("blob:");
  });

  it("is partitioned by user", () => {
    const io = memoryIO();
    saveDraft(io, "u1", "c1", { text: "mine", attachments: [] }, 1);
    expect(loadDraft(io, "u2", "c1", 2)).toBeNull();
  });

  it("deletes the entry when text and attachments are both empty", () => {
    const io = memoryIO();
    saveDraft(io, "u1", "c1", { text: "x", attachments: [] }, 1);
    saveDraft(io, "u1", "c1", { text: "", attachments: [] }, 2);
    expect(loadDraft(io, "u1", "c1", 3)).toBeNull();
    expect(io.data.size).toBe(0);
  });

  it("drops entries older than 30 days on read and keeps the 50 newest", () => {
    const io = memoryIO();
    saveDraft(io, "u1", "old", { text: "old", attachments: [] }, 0);
    expect(loadDraft(io, "u1", "old", MAX_AGE_MS + 1)).toBeNull();
    for (let i = 0; i < MAX_ENTRIES + 5; i++) saveDraft(io, "u1", `k${i}`, { text: `t${i}`, attachments: [] }, 1000 + i);
    expect(loadDraft(io, "u1", "k0", 5000)).toBeNull();
    expect(loadDraft(io, "u1", `k${MAX_ENTRIES + 4}`, 5000)?.text).toBe(`t${MAX_ENTRIES + 4}`);
  });

  it("appends late uploads to another conversation's draft, merged by file id", () => {
    const io = memoryIO();
    saveDraft(io, "u1", "c2", { text: "keep", attachments: [att("a")] }, 1);
    appendToDraft(io, "u1", "c2", [att("a"), att("b")], 2);
    expect(loadDraft(io, "u1", "c2", 3)?.attachments.map((a) => a.fileId)).toEqual(["a", "b"]);
    expect(loadDraft(io, "u1", "c2", 3)?.text).toBe("keep");
  });

  it("survives corrupt storage", () => {
    const io = memoryIO();
    io.write("riochat:drafts:u1", "{nope");
    expect(loadDraft(io, "u1", "c", 1)).toBeNull();
  });
});

describe("selections", () => {
  const skill: SkillSummaryDTO = { id: 4, scope: "user", title: "S", trigger: "s", description: "", enabled: true, sortOrder: 0 };

  it("saves, caps knowledge bases at eight and moves from the draft key", () => {
    const io = memoryIO();
    saveSelection(io, "u1", "__new__", { toolIds: [1, 2], skills: [skill], knowledgeBaseIds: Array.from({ length: 12 }, (_, i) => `kb${i}`) }, 10);
    moveSelection(io, "u1", "__new__", "c9", 20);
    expect(loadSelection(io, "u1", "__new__", 30)).toBeNull();
    const moved = loadSelection(io, "u1", "c9", 30);
    expect(moved?.toolIds).toEqual([1, 2]);
    expect(moved?.knowledgeBaseIds).toHaveLength(8);
    expect(moved?.skills[0]?.id).toBe(4);
  });

  it("does not store an empty selection", () => {
    const io = memoryIO();
    saveSelection(io, "u1", "c", { toolIds: [], skills: [], knowledgeBaseIds: [] }, 1);
    expect(io.data.size).toBe(0);
  });
});

describe("recents", () => {
  it("keeps the newest first, unique, at most twenty per kind", () => {
    const io = memoryIO();
    for (let i = 0; i < 25; i++) recordRecent(io, "model", `m${i}`, i);
    recordRecent(io, "model", "m3", 100);
    const r = readRecents(io).model;
    expect(r).toHaveLength(20);
    expect(r[0]?.id).toBe("m3");
    expect(new Set(r.map((x) => x.id)).size).toBe(20);
  });
});
