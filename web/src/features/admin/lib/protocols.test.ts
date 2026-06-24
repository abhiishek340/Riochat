import { describe, expect, it } from "vitest";
import { isAllowedSelection, kindsForProtocols, kindsJson, parseKinds, protocolLabel, resolveSelection, sortProtocols } from "./protocols";

describe("protocols", () => {
  it("labels known adapters and leaves unknown ones raw", () => {
    expect(protocolLabel("openai_chat_completions")).toBe("Chat Completions (OpenAI)");
    expect(protocolLabel("anthropic_messages")).toBe("Messages (Anthropic)");
    expect(protocolLabel("mystery_api")).toBe("mystery_api");
  });
  it("orders by vendor group then API style", () => {
    expect(sortProtocols(["openrouter_images", "xai_video", "openai_responses", "anthropic_messages", "openai_chat_completions"])).toEqual([
      "openai_chat_completions",
      "openai_responses",
      "anthropic_messages",
      "xai_video",
      "openrouter_images",
    ]);
  });
  it("derives kinds as the union, defaulting to chat", () => {
    expect(kindsForProtocols(["google_image_generation"])).toEqual(["image_gen", "image_edit"]);
    expect(kindsForProtocols(["openai_image_generations", "openai_image_edits"])).toEqual(["image_gen", "image_edit"]);
    expect(kindsForProtocols(["gemini_interactions"])).toEqual(["chat", "image_gen", "image_edit", "video_gen"]);
    expect(kindsForProtocols([])).toEqual(["chat"]);
    expect(kindsForProtocols(["nope"])).toEqual(["chat"]);
  });
  it("allows one protocol or the three supported pairs", () => {
    expect(isAllowedSelection(["openai_image_generations", "openai_image_edits"])).toBe(true);
    expect(isAllowedSelection(["xai_video", "xai_video_extensions"])).toBe(true);
    expect(isAllowedSelection(["openai_chat_completions", "openai_responses"])).toBe(false);
    expect(isAllowedSelection(["xai_image", "openai_image_edits"])).toBe(false);
  });
  it("collapses an unsupported combination to the protocol just picked", () => {
    expect(resolveSelection(["openai_chat_completions"], ["openai_chat_completions", "anthropic_messages"])).toEqual(["anthropic_messages"]);
    expect(resolveSelection(["xai_image"], ["xai_image", "xai_image_edits"])).toEqual(["xai_image", "xai_image_edits"]);
    expect(resolveSelection(["xai_image"], [])).toEqual([]);
  });
  it("round-trips kinds", () => {
    expect(parseKinds('["image_gen","chat","bogus"]')).toEqual(["chat", "image_gen"]);
    expect(parseKinds("not json")).toEqual([]);
    expect(kindsJson(["image_gen", "chat"])).toBe('["chat","image_gen"]');
  });
});
