import { describe, expect, it } from "vitest";
import type { RouteRow, Upstream } from "../llm-api";
import {
  activeModelsShown,
  blankForm,
  createBody,
  defaultsJson,
  formOf,
  groupRoutes,
  keyLines,
  keyProblem,
  keysDocument,
  newBindingProblem,
  planSave,
  rowDirty,
  updateBody,
  validateBindings,
  validateForm,
  withCompatible,
  type BindingRow,
} from "./rules";

function upstream(over: Partial<Upstream> = {}): Upstream {
  return {
    id: 1, name: "Main", baseURL: "https://api.example.com/v1", compatible: "openai", protocolDefaultsJSON: "", apiKeysMasked: "", apiKeyItems: [{ id: "k-aaaaaaaa", index: 0, keyMasked: "sk-…1", note: "", status: "active" }],
    status: "active", connectTimeoutMS: 0, readTimeoutMS: 0, streamIdleTimeoutMS: 0, cbFailureThreshold: 0, cbModelThreshold: 0, cbThresholdLogic: "or", cbDurationMin: 0, cbWindowMin: 0, headersJSON: "{}",
    modelsCount: 4, activeModelsCount: 3, circuitOpen: false, circuitUntil: "", createdAt: "", updatedAt: "", ...over,
  };
}

describe("keys", () => {
  it("reads one key per line", () => {
    expect(keyLines(" k1 \r\n\n k2\n")).toEqual(["k1", "k2"]);
  });
  it("builds the key document the API expects", () => {
    expect(JSON.parse(keysDocument(["k1", "k2"]))).toEqual({ keys: [{ key: "k1", status: "active" }, { key: "k2", status: "active" }], strategy: "round_robin" });
  });
  it("refuses to delete the last active key without a replacement", () => {
    const u = upstream();
    expect(keyProblem(u, new Set(["k-aaaaaaaa"]), [])?.key).toBe("adminUpstreams.problems.replacementKey");
    expect(keyProblem(u, new Set(["k-aaaaaaaa"]), ["new"])).toBeNull();
    expect(keyProblem(u, new Set(), [])).toBeNull();
  });
  it("does not count disabled keys as usable", () => {
    const u = upstream({ apiKeyItems: [{ id: "a", index: 0, keyMasked: "", note: "", status: "disabled" }, { id: "b", index: 1, keyMasked: "", note: "", status: "active" }] });
    expect(keyProblem(u, new Set(["b"]), [])).not.toBeNull();
  });
});

describe("form", () => {
  const ok = { ...blankForm(), name: "Main", baseURL: "https://api.example.com/v1", keys: "k1" };
  it("validates", () => {
    expect(validateForm(ok, false)).toBeNull();
    expect(validateForm({ ...ok, name: "a" }, false)).toBe("name");
    expect(validateForm({ ...ok, baseURL: "ftp://x" }, false)).toBe("baseURL");
    expect(validateForm({ ...ok, keys: " " }, false)).toBe("keysRequired");
    expect(validateForm({ ...ok, keys: "" }, true)).toBeNull();
    expect(validateForm({ ...ok, readTimeout: "ten" }, false)).toBe("number");
    expect(validateForm({ ...ok, headers: "[1]" }, false)).toBe("headers");
  });
  it("sends a JSON key document on create and leaves blank numbers out", () => {
    const body = createBody({ ...ok, keys: "k1\nk2", connectTimeout: "10000" });
    expect(JSON.parse(body.apiKeys as string).keys).toHaveLength(2);
    expect(body).toMatchObject({ name: "Main", compatible: "openai", status: "active", connectTimeoutMS: 10000, cbThresholdLogic: "or" });
    expect(body.readTimeoutMS).toBeUndefined();
    expect(body.protocolDefaultsJSON).toBeUndefined();
  });
  it("clears the protocol defaults when the compatibility changes", () => {
    const form = { ...ok, defaults: { chat: "openai_responses" as const } };
    expect(withCompatible(form, "anthropic").defaults).toEqual({});
    expect(withCompatible(form, "openai")).toBe(form);
    expect(defaultsJson({ chat: "openai_responses" })).toBe('{"chat":"openai_responses"}');
    expect(defaultsJson({})).toBe("");
  });
});

describe("updateBody", () => {
  const u = upstream({ connectTimeoutMS: 5000 });
  it("sends only what changed", () => {
    const form = formOf(u);
    expect(updateBody(u, form, new Set())).toEqual({});
    expect(updateBody(u, { ...form, name: "Renamed", readTimeout: "90000" }, new Set())).toEqual({ name: "Renamed", readTimeoutMS: 90000 });
    expect(updateBody(u, { ...form, connectTimeout: "" }, new Set())).toEqual({ connectTimeoutMS: 0 });
  });
  it("re-sends the defaults whenever the compatibility changed", () => {
    const form = withCompatible(formOf(u), "anthropic");
    expect(updateBody(u, form, new Set())).toEqual({ compatible: "anthropic", protocolDefaultsJSON: "" });
  });
  it("adds keys and deletes by sorted id", () => {
    const body = updateBody(u, { ...formOf(u), keys: "new1\nnew2" }, new Set(["z-key", "a-key"]));
    expect(JSON.parse(body.addAPIKeys as string).keys).toHaveLength(2);
    expect(body.deleteAPIKeyIDs).toEqual(["a-key", "z-key"]);
  });
  it("saves emptied headers as an empty object", () => {
    const withHeaders = upstream({ headersJSON: '{"X-A":"1"}' });
    expect(updateBody(withHeaders, { ...formOf(withHeaders), headers: "" }, new Set())).toEqual({ headersJSON: "{}" });
  });
});

describe("availability display", () => {
  it("shows zero active models for an inactive or tripped upstream", () => {
    expect(activeModelsShown(upstream())).toBe(3);
    expect(activeModelsShown(upstream({ status: "inactive" }))).toBe(0);
    expect(activeModelsShown(upstream({ circuitOpen: true }))).toBe(0);
  });
});

function route(over: Partial<RouteRow> = {}): RouteRow {
  return {
    id: 10, routeID: 10, upstreamID: 1, bindingCode: "b", platformModelID: 5, platformModelName: "gpt", modelVendor: "openai", modelKindsJSON: '["chat"]', modelIcon: "", upstreamModelName: "gpt-4o", upstreamModelVendor: "", upstreamModelIcon: "",
    upstreamModelKindsJSON: '["chat"]', suggestedProtocol: "", protocol: "openai_chat_completions", upstreamModelStatus: "active", routeStatus: "active", priority: 1, weight: 1, source: "sync", cbFailureThreshold: 0, cbDurationMin: 0,
    cbWindowMin: 0, headersJSON: "", circuitOpen: false, circuitUntil: "", createdAt: "", updatedAt: "", ...over,
  };
}

describe("groupRoutes", () => {
  it("collapses protocols of one pair into one row with the lowest id", () => {
    const rows = groupRoutes([route({ id: 12, routeID: 12, protocol: "openai_responses", routeStatus: "inactive" }), route({ id: 10, routeID: 10 })]);
    expect(rows).toHaveLength(1);
    expect(rows[0]).toMatchObject({ id: 10, protocols: ["openai_chat_completions", "openai_responses"], routeStatus: "active" });
    expect(rows[0]?.routeIds.sort()).toEqual([10, 12]);
  });
  it("keeps different pairs apart", () => {
    expect(groupRoutes([route(), route({ id: 11, routeID: 11, platformModelName: "other" })])).toHaveLength(2);
  });
});

function row(over: Partial<BindingRow> = {}): BindingRow {
  return {
    key: "k", id: 1, routeIds: [1], upstreamModelName: "gpt-4o", platformModelName: "gpt", protocols: ["openai_chat_completions"], kinds: ["chat"], routeStatus: "active", upstreamModelStatus: "active", statusTouched: false, circuitOpen: false,
    original: { platformModelName: "gpt", protocols: ["openai_chat_completions"], kinds: ["chat"], routeStatus: "active" }, ...over,
  };
}

describe("validateBindings", () => {
  it("passes tidy rows", () => {
    expect(validateBindings([row()])).toBeNull();
  });
  it("needs an upstream model name first", () => {
    expect(validateBindings([row({ upstreamModelName: " " }), row({ routeStatus: "active", platformModelName: "" })])).toEqual({ kind: "upstreamModel", row: 0 });
  });
  it("needs a platform model for an active route", () => {
    expect(validateBindings([row({ platformModelName: "  " })])).toEqual({ kind: "platformModel", row: 0 });
    expect(validateBindings([row({ platformModelName: "", routeStatus: "inactive" })])).toBeNull();
  });
  it("refuses the same triple in two rows", () => {
    expect(validateBindings([row(), row({ key: "k2", id: 2, routeIds: [2] })])).toEqual({ kind: "duplicate", row: 1 });
    expect(validateBindings([row(), row({ key: "k2", protocols: ["openai_responses"] })])).toBeNull();
  });
});

describe("planSave", () => {
  it("skips untouched rows", () => {
    expect(planSave([row()])).toEqual({ upserts: [], deletes: [] });
    expect(rowDirty(row())).toBe(false);
  });
  it("upserts a renamed row, with the status only when touched", () => {
    const changed = row({ platformModelName: "gpt-new" });
    expect(planSave([changed]).upserts[0]).toMatchObject({ platformModelName: "gpt-new", routeIDs: [1], protocols: ["openai_chat_completions"], kindsJSON: '["chat"]' });
    expect(planSave([changed]).upserts[0]?.status).toBeUndefined();
    expect(planSave([{ ...changed, routeStatus: "inactive", statusTouched: true }]).upserts[0]?.status).toBe("inactive");
  });
  it("deletes the routes of an inactive row whose platform model was cleared", () => {
    const cleared = row({ platformModelName: "", routeStatus: "inactive", routeIds: [1, 2], original: { platformModelName: "gpt", protocols: [], kinds: ["chat"], routeStatus: "active" } });
    expect(planSave([cleared])).toEqual({ upserts: [], deletes: [1, 2] });
  });
  it("derives the kinds from the protocols", () => {
    const image = row({ protocols: ["google_image_generation"], platformModelName: "img" });
    expect(planSave([image]).upserts[0]?.kindsJSON).toBe('["image_gen","image_edit"]');
  });
});

describe("new binding", () => {
  it("needs both names", () => {
    expect(newBindingProblem("", "x")).toBe("upstreamModel");
    expect(newBindingProblem("x", " ")).toBe("platformModel");
    expect(newBindingProblem("x", "y")).toBeNull();
  });
});
