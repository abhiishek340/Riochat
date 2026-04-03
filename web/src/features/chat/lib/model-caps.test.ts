import { describe, expect, it } from "vitest";
import type { ModelDTO, ModelOptionPolicyDTO } from "../types";
import { isToolPayloadEnabled } from "./options";
import { mediaControls, modelDefaultOptions, modelLabel, parseModelCaps, pathPolicyStatus } from "./model-caps";

function model(partial: Partial<ModelDTO>): ModelDTO {
  return {
    platformModelName: "m",
    vendor: "",
    vendorName: "",
    vendorIcon: "",
    displayGroupID: null,
    displayGroupName: "",
    displayGroupIcon: "",
    kindsJSON: "[]",
    icon: "",
    protocolsJSON: "[]",
    capabilitiesJSON: "{}",
    description: "",
    sortOrder: 0,
    pricing: null,
    reasoning: null,
    controls: null,
    inputModalities: null,
    outputModalities: null,
    contextWindow: null,
    source: "platform",
    providerName: "",
    providerIcon: "",
    ...partial,
  };
}

const policy: ModelOptionPolicyDTO = {
  mode: "allowlist",
  allowedPathsJSON: JSON.stringify({ default: ["temperature"], openai_image_generations: ["size", "quality", "n"], xai_video_extensions: ["duration"] }),
  deniedPathsJSON: JSON.stringify({ default: ["model"] }),
  nativeTools: [
    { protocol: "openai_responses", provider: "OpenAI", type: "web_search", toolKey: "web_search", label: "Web search", description: "", payload: { type: "web_search" }, defaultEnabled: false },
    { protocol: "anthropic_messages", provider: "Anthropic", type: "web_search_20250305", toolKey: "web_search_20250305", label: "Web search", description: "", payload: { type: "web_search_20250305" }, defaultEnabled: false },
  ],
};

describe("parseModelCaps", () => {
  it("survives broken JSON", () => {
    const caps = parseModelCaps(model({ capabilitiesJSON: "{nope", protocolsJSON: "x" }));
    expect(caps.defaultOptions).toEqual({});
    expect(caps.nativeTools).toEqual([]);
    expect(caps.videoExtension.available).toBe(false);
  });

  it("offers video extension only for the extension protocol", () => {
    const yes = parseModelCaps(model({ protocolsJSON: '["xai_video_extensions"]' }));
    expect(yes.videoExtension.available).toBe(true);
    expect(yes.videoExtension.defaultOptions).toEqual({ duration: 6 });
    expect(yes.videoExtension.optionControls[0]?.options.map((o) => o.value)).toEqual(["2", "3", "4", "5", "6", "7", "8", "9", "10"]);
    const off = parseModelCaps(
      model({ protocolsJSON: '["xai_video_extensions"]', capabilitiesJSON: '{"mediaTasks":{"video_extension":{"enabled":false}}}' }),
    );
    expect(off.videoExtension.available).toBe(false);
  });

  it("resolves native tool keys against the policy catalog and the model protocols", () => {
    const caps = parseModelCaps(model({ protocolsJSON: '["openai_responses"]', capabilitiesJSON: '{"nativeToolKeys":["web_search","web_search_20250305"]}' }), policy);
    expect(caps.nativeTools.map((t) => t.key)).toEqual(["web_search"]);
  });

  it("merges protocol variants of one tool and merges default-enabled tools into defaults", () => {
    const caps = parseModelCaps(
      model({
        capabilitiesJSON: JSON.stringify({
          defaultOptions: { temperature: 1 },
          nativeTools: [
            { key: "search", provider: "P", protocol: "a", payload: { type: "s1" }, defaultEnabled: true },
            { key: "search", provider: "P", protocol: "b", payload: { type: "s2" } },
            { key: "off", provider: "P", payload: { type: "x" }, enabled: false },
          ],
        }),
      }),
    );
    expect(caps.nativeTools).toHaveLength(1);
    expect(caps.nativeTools[0]?.payloads).toEqual([{ type: "s1" }, { type: "s2" }]);
    const defaults = modelDefaultOptions(caps);
    expect(defaults.temperature).toBe(1);
    expect(isToolPayloadEnabled(defaults, { type: "s1" })).toBe(true);
    expect(isToolPayloadEnabled(defaults, { type: "s2" })).toBe(true);
  });
});

describe("media controls", () => {
  it("infers controls from default leaves, limited by the policy", () => {
    const caps = parseModelCaps(
      model({ protocolsJSON: '["openai_image_generations"]', capabilitiesJSON: '{"defaultOptions":{"size":"1024x1024","n":1,"quality":"high","secret":"x","nested":{"flag":true}}}' }),
    );
    const controls = mediaControls(caps, policy);
    const byPath = Object.fromEntries(controls.map((c) => [c.path, c]));
    expect(byPath.size?.type).toBe("select");
    expect(byPath.n?.type).toBe("number");
    expect(byPath.quality?.options.map((o) => o.value)).toContain("high");
    expect(byPath.secret).toBeUndefined();
    expect(byPath["nested.flag"]).toBeUndefined();
  });
});

describe("policy status", () => {
  it("classifies paths", () => {
    expect(pathPolicyStatus(policy, ["openai_image_generations"], "size")).toBe("passes");
    expect(pathPolicyStatus(policy, ["openai_image_generations"], "bogus")).toBe("ignored");
    expect(pathPolicyStatus(policy, ["openai_image_generations", "xai_video_extensions"], "size")).toBe("route");
    expect(pathPolicyStatus(policy, [], "model")).toBe("ignored");
    expect(pathPolicyStatus({ ...policy, mode: "disabled" }, [], "temperature")).toBe("ignored");
  });
});

describe("modelLabel", () => {
  it("drops a vendor prefix", () => {
    expect(modelLabel("openai/gpt-4o")).toBe("gpt-4o");
    expect(modelLabel("claude")).toBe("claude");
  });
});
