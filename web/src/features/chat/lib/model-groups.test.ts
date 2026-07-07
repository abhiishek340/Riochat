import { describe, expect, it } from "vitest";
import type { ModelDTO } from "../types";
import { currentPeriod, formatContext, groupModels, matchesModel, modalitiesOf } from "./model-groups";

function m(name: string, over: Partial<ModelDTO> = {}): ModelDTO {
  return {
    platformModelName: name, vendor: "", vendorName: "", vendorIcon: "", displayGroupID: null, displayGroupName: "", displayGroupIcon: "", kindsJSON: "[]", icon: "",
    protocolsJSON: "[]", capabilitiesJSON: "{}", description: "", sortOrder: 0, pricing: null, reasoning: null, controls: null, inputModalities: null,
    outputModalities: null, contextWindow: null, source: "platform", providerName: "", providerIcon: "", ...over,
  };
}

describe("groupModels", () => {
  it("groups platform models by display group and puts personal providers last", () => {
    const groups = groupModels(
      [
        m("a", { displayGroupID: 2, displayGroupName: "Beta", sortOrder: 20 }),
        m("b", { displayGroupID: 1, displayGroupName: "Alpha", sortOrder: 10 }),
        m("c", { displayGroupID: 1, displayGroupName: "Alpha", sortOrder: 5 }),
        m("mine", { source: "personal", providerName: "My provider" }),
      ],
      "Other",
    );
    expect(groups.map((g) => g.name)).toEqual(["Alpha", "Beta", "My provider"]);
    expect(groups[0]?.models.map((x) => x.platformModelName)).toEqual(["c", "b"]);
    expect(groups[2]?.byok).toBe(true);
  });

  it("falls back to the vendor name and an other group", () => {
    const groups = groupModels([m("x", { vendorName: "Acme" }), m("y")], "Other");
    expect(groups.map((g) => g.name).sort()).toEqual(["Acme", "Other"]);
  });
});

describe("helpers", () => {
  it("searches several fields", () => {
    expect(matchesModel(m("gpt-x", { vendorName: "Acme" }), "acme")).toBe(true);
    expect(matchesModel(m("gpt-x"), "claude")).toBe(false);
    expect(matchesModel(m("gpt-x"), "")).toBe(true);
  });
  it("orders modalities and defaults to text", () => {
    expect(modalitiesOf(m("a", { inputModalities: ["video", "text", "image", "junk"] }))).toEqual(["text", "image", "video"]);
    expect(modalitiesOf(m("a"))).toEqual(["text"]);
  });
  it("formats context windows", () => {
    expect(formatContext(128000)).toBe("128K");
    expect(formatContext(1_000_000)).toBe("1M");
    expect(formatContext(null)).toBe("");
  });
  it("finds the active schedule period in the period's offset", () => {
    const periods = [
      { weekdays: [1, 2, 3, 4, 5], start: "09:00", end: "18:00" },
      { weekdays: [], start: "22:00", end: "06:00" },
    ];
    // Monday 10:00 UTC with a +0 offset.
    expect(currentPeriod(periods, 0, new Date("2026-06-15T10:00:00Z"))).toBe(0);
    // Monday 10:00 UTC is 18:00 at +8: the first period just ended.
    expect(currentPeriod(periods, 480, new Date("2026-06-15T10:00:00Z"))).toBe(-1);
    // Overnight period wraps midnight.
    expect(currentPeriod(periods, 0, new Date("2026-06-14T23:30:00Z"))).toBe(1);
    expect(currentPeriod([], 0, new Date())).toBe(-1);
  });
});
