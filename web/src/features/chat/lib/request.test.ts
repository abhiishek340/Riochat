import { describe, expect, it } from "vitest";
import type { ChatAttachment } from "../types";
import { buildChatBody, buildMediaBody, buildTempRequest, EMPTY_SELECTIONS, wireOptions, type SendInput } from "./request";

const att = (id: string): ChatAttachment => ({
  fileId: id, fileName: `${id}.png`, mimeType: "image/png", detectedMime: "image/png", fileCategory: "image", fileSize: 1,
  durationSeconds: 0, kind: "image", processingStatus: "ready", processingReady: true, processingErrorCode: "", processingErrorMessage: "",
});

const base = (p: Partial<SendInput> = {}): SendInput => ({
  task: "chat",
  content: "hi",
  attachments: [],
  model: "m",
  options: {},
  clientRunId: "run_x",
  parentId: null,
  sourceId: null,
  branchReason: "default",
  selections: EMPTY_SELECTIONS,
  uiStates: [],
  ...p,
});

describe("wireOptions", () => {
  it("removes reserved keys and returns nothing when empty or disabled", () => {
    expect(wireOptions({ prompt: "x", temperature: 1 })).toEqual({ temperature: 1 });
    expect(wireOptions({ prompt: "x" })).toBeUndefined();
    expect(wireOptions({ temperature: 1 }, "disabled")).toBeUndefined();
  });
});

describe("buildChatBody", () => {
  it("sends the minimal body for plain text", () => {
    expect(buildChatBody(base())).toEqual({
      content: "hi", contentType: "text", model: "m", clientRunID: "run_x", branchReason: "default", knowledgeBaseIDs: [],
    });
  });

  it("marks attachments as mixed and carries branch fields and selections", () => {
    const body = buildChatBody(
      base({
        attachments: [att("f1")],
        parentId: "p",
        sourceId: "s",
        branchReason: "retry",
        controls: { reasoning: "high" },
        options: { temperature: 0.2 },
        selections: { toolIds: [1, 2], skillIds: [3], knowledgeBaseIds: ["kb"], uiComponentIds: [4], visualPrompt: true },
        uiStates: [{ messagePublicID: "m1", blockID: "b", state: { a: 1 } }],
      }),
    );
    expect(body).toMatchObject({
      contentType: "mixed", fileIDs: ["f1"], parentMessagePublicID: "p", sourceMessagePublicID: "s", branchReason: "retry",
      controls: { reasoning: "high" }, options: { temperature: 0.2 }, selectedToolIDs: [1, 2], skillIDs: [3], knowledgeBaseIDs: ["kb"],
      htmlVisualPrompt: true, uiComponentIDs: [4], uiBlockVersion: 2,
    });
    expect(body.uiStates).toHaveLength(1);
  });

  it("keeps the 32 newest ui states", () => {
    const states = Array.from({ length: 40 }, (_, i) => ({ messagePublicID: `m${i}`, blockID: "b", state: { i } }));
    const sent = buildChatBody(base({ uiStates: states })).uiStates as { messagePublicID: string }[];
    expect(sent).toHaveLength(32);
    expect(sent[0]?.messagePublicID).toBe("m8");
  });
});

describe("buildMediaBody", () => {
  it("uses prompt and file ids for image tasks", () => {
    expect(buildMediaBody(base({ task: "image_edit", attachments: [att("f1")], options: { size: "1024x1024" } }))).toEqual({
      prompt: "hi", model: "m", clientRunID: "run_x", branchReason: "default", options: { size: "1024x1024" }, fileIDs: ["f1"],
    });
  });
  it("sends only a clamped duration and the source video for an extension", () => {
    const body = buildMediaBody(base({ task: "video_extension", attachments: [att("v1")], options: { duration: 12, size: "x" } }));
    expect(body.options).toEqual({ duration: 6 });
    expect(body.sourceVideoFileID).toBe("v1");
    expect(body.fileIDs).toBeUndefined();
  });
});

describe("buildTempRequest", () => {
  const file = (name: string, type = "image/png") => new File(["x"], name, { type });
  const common = {
    sessionId: "s", clientRunId: "run_x", model: "m", options: {}, selections: EMPTY_SELECTIONS, uiStates: [],
    omittedNote: "[earlier omitted]", attachmentNote: (n: string[]) => `[omitted: ${n.join(", ")}]`,
  };

  it("keeps the last 99 history entries plus the new turn and notes the cut", () => {
    const history = Array.from({ length: 120 }, (_, i) => ({ role: (i % 2 ? "assistant" : "user") as "user" | "assistant", content: `m${i}` }));
    const req = buildTempRequest({ ...common, history, turn: { role: "user", content: "new" } });
    const messages = req.payload.messages as { content: string }[];
    expect(messages).toHaveLength(100);
    expect(messages[0]?.content.startsWith("[earlier omitted]")).toBe(true);
    expect(messages[0]?.content.endsWith("m21")).toBe(true);
    expect(messages.at(-1)?.content).toBe("new");
    expect(req.truncated).toBe(true);
  });

  it("limits files to 20 and images to 10, preferring newest, and names the omitted ones", () => {
    const history = [{ role: "user" as const, content: "old", files: Array.from({ length: 12 }, (_, i) => file(`old${i}.png`)) }];
    const turn = { role: "user" as const, content: "new", files: Array.from({ length: 4 }, (_, i) => file(`new${i}.png`)) };
    const req = buildTempRequest({ ...common, history, turn });
    expect(req.files).toHaveLength(10);
    expect(req.files.filter((f) => f.messageIndex === 1)).toHaveLength(4);
    const oldNote = (req.payload.messages as { content: string }[])[0]?.content ?? "";
    expect(oldNote).toContain("[omitted: old0.png");
  });

  it("rebases ui state indexes after truncation", () => {
    const history = Array.from({ length: 100 }, (_, i) => ({ role: (i % 2 ? "assistant" : "user") as "user" | "assistant", content: `m${i}` }));
    const req = buildTempRequest({
      ...common,
      history,
      turn: { role: "user", content: "n" },
      uiStates: [
        { messageIndex: 0, blockID: "gone", state: {} },
        { messageIndex: 99, blockID: "kept", state: { a: 1 } },
      ],
    });
    expect(req.payload.uiStates).toEqual([{ messageIndex: 98, blockID: "kept", state: { a: 1 } }]);
  });
});
