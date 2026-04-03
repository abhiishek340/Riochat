import { describe, expect, it } from "vitest";
import {
  decideTask,
  extensionOptions,
  isChatCapable,
  parseKinds,
  placeholderShape,
  requestedResponseType,
  taskEndpoint,
  type AttachmentFacts,
} from "./task-decision";

const img: AttachmentFacts = { fileCategory: "image", mimeType: "image/png", detectedMime: "image/png" };
const mp4: AttachmentFacts = { fileCategory: "video", mimeType: "video/mp4", detectedMime: "video/mp4" };
const pdf: AttachmentFacts = { fileCategory: "pdf", mimeType: "application/pdf", detectedMime: "application/pdf" };
const model = (kinds: string[], ext = false) => ({ kinds: new Set(kinds), supportsVideoExtension: ext });

describe("decideTask", () => {
  it("treats empty or unknown kinds as chat", () => {
    expect(isChatCapable(new Set())).toBe(true);
    expect(isChatCapable(new Set(["mystery"]))).toBe(true);
    expect(isChatCapable(new Set(["audio"]))).toBe(true);
    expect(isChatCapable(new Set(["image_gen"]))).toBe(false);
  });

  it("blocks image edit without an input image (B8)", () => {
    expect(decideTask({ model: model(["image_edit"]), attachments: [], requested: null })).toEqual({ task: "image_edit", blocked: "B8" });
  });

  it("uses chat for a chat+image_gen model given one image", () => {
    expect(decideTask({ model: model(["chat", "image_gen"]), attachments: [img], requested: null })).toEqual({ task: "chat", blocked: null });
  });

  it("blocks two images for video generation (B5)", () => {
    expect(decideTask({ model: model(["video_gen"]), attachments: [img, img], requested: null })).toEqual({
      task: "video_generation",
      blocked: "B5",
    });
  });

  it("treats a video as an ordinary attachment for a chat model without extension", () => {
    expect(decideTask({ model: model(["chat"]), attachments: [mp4], requested: null })).toEqual({ task: "chat", blocked: null });
  });

  it("needs exactly one mp4 for extension (B1)", () => {
    expect(decideTask({ model: model(["chat"], true), attachments: [mp4, img], requested: null }).blocked).toBe("B1");
    expect(decideTask({ model: model(["chat"], true), attachments: [mp4], requested: null })).toEqual({ task: "video_extension", blocked: null });
  });

  it("flags extension on a model that cannot extend (B2)", () => {
    expect(decideTask({ model: model(["video_gen"], false), attachments: [mp4], requested: null }).blocked).toBe("B2");
  });

  it("rejects documents for image tasks (B3) and prefers edit when images exist", () => {
    expect(decideTask({ model: model(["image_edit"]), attachments: [img, pdf], requested: null })).toEqual({ task: "image_edit", blocked: "B3" });
    expect(decideTask({ model: model(["image_gen"]), attachments: [pdf], requested: null }).task).toBe("image_generation");
  });

  it("honours the requested response type", () => {
    const m = model(["chat", "image_gen", "video_gen"]);
    expect(decideTask({ model: m, attachments: [], requested: "image" }).task).toBe("image_generation");
    expect(decideTask({ model: m, attachments: [], requested: "video" }).task).toBe("video_generation");
    expect(decideTask({ model: m, attachments: [], requested: "text" }).task).toBe("chat");
  });

  it("reports a model that supports nothing it was given (B9/B6/B7)", () => {
    expect(decideTask({ model: model(["image_gen"]), attachments: [pdf], requested: null }).blocked).toBe("B3");
    expect(decideTask({ model: model(["image_gen"]), attachments: [mp4], requested: null }).blocked).toBe("B2");
    expect(decideTask({ model: model(["image_gen"]), attachments: [img], requested: null }).blocked).toBe("B6");
    expect(decideTask({ model: model(["audio_only"]), attachments: [], requested: null }).task).toBe("chat");
  });

  it("sends a plain chat for a mixed image and document set on a chat model", () => {
    expect(decideTask({ model: model(["chat"]), attachments: [img, pdf], requested: null })).toEqual({ task: "chat", blocked: null });
  });
});

describe("helpers", () => {
  it("parses kinds defensively", () => {
    expect([...parseKinds('["chat","Image_Gen"]')]).toEqual(["chat", "image_gen"]);
    expect(parseKinds("nope").size).toBe(0);
  });

  it("reads the requested response type with priority video > image > text", () => {
    expect(requestedResponseType({ response_format: { type: "image" } })).toBe("image");
    expect(requestedResponseType({ response_format: [{ type: "text" }, { type: "video" }, { type: "image" }] })).toBe("video");
    expect(requestedResponseType({})).toBeNull();
  });

  it("clamps video extension duration", () => {
    expect(extensionOptions({ duration: 12 })).toEqual({ duration: 6 });
    expect(extensionOptions({ duration: "8" })).toEqual({ duration: 8 });
    expect(extensionOptions({ duration: 2.5 })).toEqual({ duration: 6 });
    expect(extensionOptions(undefined)).toEqual({ duration: 6 });
  });

  it("derives the placeholder shape", () => {
    expect(placeholderShape({ size: "1024x768" })).toBe("wide");
    expect(placeholderShape({ size: "768x1024" })).toBe("portrait");
    expect(placeholderShape({ size: "512x512" })).toBe("square");
    expect(placeholderShape({ size: "auto" })).toBe("wide");
  });

  it("maps tasks to endpoints", () => {
    expect(taskEndpoint("abc", "chat")).toBe("/conversations/abc/messages/stream");
    expect(taskEndpoint("abc", "video_extension")).toBe("/conversations/abc/media/videos/extensions/stream");
  });
});
