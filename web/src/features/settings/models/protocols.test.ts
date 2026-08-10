import { describe, expect, it } from "vitest";
import { buildProtocolChoices, protocolKind, protocolLabel, splitProtocols, withCurrentChoice } from "./protocols";

const SERVER_ORDER = [
  "openai_chat_completions",
  "openai_responses",
  "openai_image_generations",
  "openai_image_edits",
  "anthropic_messages",
  "xai_image",
  "xai_image_edits",
  "xai_video",
  "xai_video_extensions",
];

describe("labels", () => {
  it("names endpoint and vendor, and shows unknown ids verbatim", () => {
    expect(protocolLabel("anthropic_messages")).toBe("Messages (Anthropic)");
    expect(protocolLabel("openrouter_images")).toBe("Images (OpenRouter)");
    expect(protocolLabel("brand_new_protocol")).toBe("brand_new_protocol");
    expect(protocolKind("xai_video")).toBe("video");
    expect(protocolKind("openai_image_edits")).toBe("image");
    expect(protocolKind("brand_new_protocol")).toBe("chat");
  });
});

describe("buildProtocolChoices", () => {
  const choices = buildProtocolChoices(SERVER_ORDER);
  const values = choices.map((c) => c.value);

  it("puts a combined choice right before the first protocol of its pair", () => {
    const at = values.indexOf("openai_image_generations+openai_image_edits");
    expect(at).toBeGreaterThan(-1);
    expect(values[at + 1]).toBe("openai_image_generations");
    expect(values.indexOf("xai_video+xai_video_extensions") + 1).toBe(values.indexOf("xai_video"));
  });
  it("labels the combinations", () => {
    expect(choices.find((c) => c.value === "xai_image+xai_image_edits")?.label).toBe("Images Generations + Edits (xAI)");
    expect(choices.find((c) => c.value === "xai_video+xai_video_extensions")?.label).toBe("Video Generations + Extensions (xAI)");
  });
  it("groups by vendor in order of first appearance", () => {
    const vendors = choices.map((c) => protocolLabel(c.members[0] as string).split("(")[1]);
    const seen = vendors.filter((v, i) => v !== vendors[i - 1]);
    expect(seen).toEqual(["OpenAI)", "Anthropic)", "xAI)"]);
  });
  it("skips a pair whose second member is not offered", () => {
    expect(buildProtocolChoices(["openai_image_generations"]).map((c) => c.value)).toEqual(["openai_image_generations"]);
  });
});

describe("withCurrentChoice", () => {
  it("keeps a value the server no longer offers", () => {
    const base = buildProtocolChoices(["anthropic_messages"]);
    expect(withCurrentChoice(base, "anthropic_messages")).toHaveLength(1);
    const extended = withCurrentChoice(base, "legacy_proto");
    expect(extended.at(-1)?.value).toBe("legacy_proto");
    expect(splitProtocols("a+b")).toEqual(["a", "b"]);
  });
});
