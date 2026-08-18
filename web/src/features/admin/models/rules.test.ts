import { describe, expect, it } from "vitest";
import type { PlatformModel } from "../lib/wire";
import {
  breakerEnabled,
  clampActive,
  createModelBody,
  flattenOrder,
  groupForOrder,
  iconFileProblem,
  blankModelForm,
  blankSource,
  leavesList,
  nonNegative,
  parsePositiveInt,
  protocolChoices,
  protocolTargets,
  readIconUsage,
  readVendorUsage,
  sourceAvailable,
  sourceBadge,
  sourceBody,
  syncOutcome,
  updateModelBody,
  validateModelForm,
  validateSources,
  validateVendor,
  withBreaker,
  withSourceChange,
  withSourceRemoved,
  withStatus,
} from "./rules";

const model = (over: Partial<PlatformModel> = {}): PlatformModel =>
  ({ id: 1, platformModelName: "m", vendor: "openai", vendorName: "OpenAI", vendorIcon: "", displayGroupID: null, displayGroupName: "", displayGroupIcon: "", kindsJSON: "[]", icon: "", capabilitiesJSON: "", contextWindow: 0, systemPrompt: "", accessScope: "public", status: "active", description: "", cbPolicyMode: "default", cbFailureThreshold: 0, cbDurationMin: 0, cbWindowMin: 0, sortOrder: 0, sourceCount: 2, activeSourceCount: 1, protocolsJSON: "[]", upstreamNamesJSON: "[]", createdAt: "", updatedAt: "", ...over }) as PlatformModel;

const ok = { status: "active", upstreamStatus: "active", upstreamModelStatus: "active", circuitOpen: false };

describe("availability", () => {
  it("needs everything active and the circuit closed", () => {
    expect(sourceAvailable("active", ok)).toBe(true);
    expect(sourceAvailable("inactive", ok)).toBe(false);
    expect(sourceAvailable("active", { ...ok, status: "inactive" })).toBe(false);
    expect(sourceAvailable("active", { ...ok, upstreamStatus: "inactive" })).toBe(false);
    expect(sourceAvailable("active", { ...ok, upstreamModelStatus: "inactive" })).toBe(false);
    expect(sourceAvailable("active", { ...ok, circuitOpen: true })).toBe(false);
  });
  it("ignores circuit state while the global breaker is off", () => {
    expect(sourceAvailable("active", { ...ok, circuitOpen: true }, false)).toBe(true);
  });
  it("moves the active count by one and clamps it", () => {
    const m = model();
    expect(withSourceChange(m, ok, { ...ok, status: "inactive" }).activeSourceCount).toBe(0);
    expect(withSourceChange(m, { ...ok, status: "inactive" }, ok).activeSourceCount).toBe(2);
    expect(withSourceChange(model({ activeSourceCount: 0 }), ok, { ...ok, status: "inactive" }).activeSourceCount).toBe(0);
    expect(withSourceChange(model({ activeSourceCount: 2 }), { ...ok, status: "inactive" }, ok).activeSourceCount).toBe(2);
    expect(withSourceChange(m, ok, { ...ok, priority: 1 } as typeof ok)).toBe(m);
    expect(clampActive(m, 9)).toBe(2);
    expect(clampActive(m, -1)).toBe(0);
  });
  it("counts nothing while the model itself is inactive", () => {
    const m = model({ status: "inactive", activeSourceCount: 0 });
    expect(withSourceChange(m, ok, { ...ok, status: "inactive" })).toBe(m);
  });
  it("shrinks both counts when a source is removed", () => {
    expect(withSourceRemoved(model(), ok)).toMatchObject({ sourceCount: 1, activeSourceCount: 0 });
    expect(withSourceRemoved(model(), { ...ok, status: "inactive" })).toMatchObject({ sourceCount: 1, activeSourceCount: 1 });
    expect(withSourceRemoved(model({ sourceCount: 0, activeSourceCount: 0 }), ok)).toMatchObject({ sourceCount: 0, activeSourceCount: 0 });
  });
  it("labels models without sources and without available sources", () => {
    expect(sourceBadge(model({ sourceCount: 0, activeSourceCount: 0 }))).toBe("none");
    expect(sourceBadge(model({ activeSourceCount: 0 }))).toBe("notEnabled");
    expect(sourceBadge(model())).toBe("ok");
  });
});

describe("status toggle", () => {
  it("disabling empties the active count, enabling keeps it", () => {
    expect(withStatus(model(), "inactive")).toMatchObject({ status: "inactive", activeSourceCount: 0 });
    expect(withStatus(model(), "active").activeSourceCount).toBe(1);
  });
  it("leaves the list when it no longer matches the filter", () => {
    expect(leavesList("active", "inactive")).toBe(true);
    expect(leavesList("active", "active")).toBe(false);
    expect(leavesList("", "inactive")).toBe(false);
  });
  it("targets only models with sources for a protocol change", () => {
    expect(protocolTargets([model({ id: 1 }), model({ id: 2, sourceCount: 0 })]).map((m) => m.id)).toEqual([1]);
  });
});

describe("numbers", () => {
  it("parses positive integers strictly", () => {
    expect(parsePositiveInt(" 12 ")).toBe(12);
    for (const bad of ["0", "-1", "1.5", "", "a", "1e3"]) expect(parsePositiveInt(bad), bad).toBeNull();
    expect(nonNegative("7")).toBe(7);
    expect(nonNegative("-3")).toBe(0);
    expect(nonNegative("x")).toBe(0);
  });
});

describe("model body", () => {
  it("omits display group and kinds on create when unset", () => {
    const form = { ...blankModelForm(), name: " gpt-5 " };
    const body = createModelBody(form, "");
    expect(body.platformModelName).toBe("gpt-5");
    expect("displayGroupID" in body).toBe(false);
    expect("kindsJSON" in body).toBe(false);
    expect(createModelBody({ ...form, displayGroupID: 4, kinds: ["image_gen", "chat"] }, "{}")).toMatchObject({ displayGroupID: 4, kindsJSON: '["chat","image_gen"]' });
  });
  it("sends everything on update and turns bad numbers into 0", () => {
    const body = updateModelBody({ ...blankModelForm(), name: "x1", cbFailureThreshold: "abc", cbDurationMin: "5", systemPrompt: "  " }, "");
    expect(body).toMatchObject({ displayGroupID: 0, kindsJSON: "[]", cbFailureThreshold: 0, cbDurationMin: 5, systemPrompt: "" });
  });
  it("requires a name of 2 to 128 characters", () => {
    expect(validateModelForm({ ...blankModelForm(), name: "a" })).not.toBeNull();
    expect(validateModelForm({ ...blankModelForm(), name: "ab" })).toBeNull();
    expect(validateModelForm({ ...blankModelForm(), name: "a".repeat(129) })).not.toBeNull();
  });
});

describe("initial sources", () => {
  const row = (over = {}) => ({ ...blankSource("k"), upstreamID: 1, upstreamModelID: 2, protocol: "openai_responses", ...over });
  it("ignores empty rows and checks each field in turn", () => {
    expect(validateSources([blankSource("a")])).toBeNull();
    expect(validateSources([row({ upstreamID: 0 })])).toEqual({ index: 0, issue: "upstream" });
    expect(validateSources([row({ upstreamModelID: 0 })])).toEqual({ index: 0, issue: "model" });
    expect(validateSources([row({ protocol: "" })])).toEqual({ index: 0, issue: "protocol" });
    expect(validateSources([row({ priority: "0" })])).toEqual({ index: 0, issue: "priority" });
    expect(validateSources([row({ weight: "x" })])).toEqual({ index: 0, issue: "weight" });
  });
  it("flags a repeated upstream, model and protocol", () => {
    expect(validateSources([row(), row({ key: "b" })])).toEqual({ index: 1, issue: "duplicate" });
    expect(validateSources([row(), row({ key: "b", protocol: "openai_chat_completions" })])).toBeNull();
  });
  it("builds the bind body with defaults", () => {
    expect(sourceBody(row())).toEqual({ upstreamID: 1, upstreamModelID: 2, protocol: "openai_responses", priority: 1, weight: 1, status: "active" });
  });
});

describe("order", () => {
  it("groups by display group, else vendor, in order of first appearance", () => {
    const list = [
      model({ id: 1, vendor: "openai" }),
      model({ id: 2, vendor: "anthropic", vendorName: "Anthropic" }),
      model({ id: 3, vendor: "openai" }),
      model({ id: 4, displayGroupID: 9, displayGroupName: "Fast" }),
    ];
    const groups = groupForOrder(list);
    expect(groups.map((g) => [g.key, g.models.map((m) => m.id)])).toEqual([
      ["v:openai", [1, 3]],
      ["v:anthropic", [2]],
      ["g:9", [4]],
    ]);
    expect(flattenOrder(groups)).toEqual([1, 3, 2, 4]);
  });
});

describe("global circuit breaker", () => {
  it("is on only for a strict true", () => {
    expect(breakerEnabled('{"enabled":true}')).toBe(true);
    for (const v of ['{"enabled":"true"}', '{"enabled":1}', "{", "", undefined, "[]", "null"]) expect(breakerEnabled(v), String(v)).toBe(false);
  });
  it("keeps other keys when writing", () => {
    expect(JSON.parse(withBreaker('{"enabled":false,"failureThreshold":5}', true))).toEqual({ enabled: true, failureThreshold: 5 });
    expect(JSON.parse(withBreaker("not json", true))).toEqual({ enabled: true });
    expect(JSON.parse(withBreaker(undefined, false))).toEqual({ enabled: false });
  });
});

describe("catalog sync outcome", () => {
  it("distinguishes success, one failure and two", () => {
    expect(syncOutcome({ ok: true }, { ok: true })).toEqual({ kind: "success" });
    expect(syncOutcome({ ok: true }, { ok: false, error: "x" })).toEqual({ kind: "warning", failed: "openrouter", error: "x" });
    expect(syncOutcome({ ok: false, error: "y" }, { ok: true })).toEqual({ kind: "warning", failed: "catalog", error: "y" });
    expect(syncOutcome({ ok: false, error: "y" }, { ok: false, error: "x" })).toEqual({ kind: "error", errors: ["y", "x"] });
  });
});

describe("presentation", () => {
  it("validates vendor input", () => {
    expect(validateVendor({ key: "Acme-1", name: "Acme" }, true)).toBeNull();
    expect(validateVendor({ key: "-bad", name: "Acme" }, true)).toBe("key");
    expect(validateVendor({ key: "", name: "Acme" }, true)).toBe("key");
    expect(validateVendor({ key: "", name: " " }, false)).toBe("name");
  });
  it("reads usage details", () => {
    const usage = readVendorUsage({ referenceCount: 7, models: [{ platformModelName: "a" }, { platformModelName: "b" }] });
    expect(usage).toEqual({ count: 7, names: ["a", "b"], more: 5 });
    expect(readVendorUsage(null)).toEqual({ count: 0, names: [], more: 0 });
    expect(readIconUsage({ referenceCount: 3 })).toBe(3);
    expect(readIconUsage("x")).toBe(0);
  });
  it("checks icon files", () => {
    expect(iconFileProblem({ type: "image/png", size: 10 })).toBeNull();
    expect(iconFileProblem({ type: "image/gif", size: 10 })).toBe("type");
    expect(iconFileProblem({ type: "image/webp", size: 0 })).toBe("size");
    expect(iconFileProblem({ type: "image/jpeg", size: 1024 * 1024 + 1 })).toBe("size");
    expect(iconFileProblem({ type: "image/jpeg", size: 1024 * 1024 })).toBeNull();
  });
});

describe("protocol choices", () => {
  it("follow the kinds of the model and keep the current value", () => {
    const video = protocolChoices(["video_extension"]);
    expect(video).toEqual(["xai_video_extensions"]);
    expect(protocolChoices(["video_extension"], "legacy_one")).toContain("legacy_one");
    expect(protocolChoices([]).length).toBeGreaterThan(10);
    expect(protocolChoices(["chat"])).toContain("openai_responses");
    expect(protocolChoices(["chat"])).not.toContain("xai_video");
  });
});
