import { describe, expect, it } from "vitest";
import type { RemoteItem, SyncPlan, SyncResult } from "../llm-api";
import { allowEmpty, buildRows, canApply, defaultSelection, hasCatalogChanges, importItems, needsConfirmation, outcomeCounts, rowsForOutcomes, searchRows } from "./sync";

function item(name: string, over: Partial<RemoteItem> = {}): RemoteItem {
  return { upstreamModelName: name, suggestedPlatformModelName: name, suggestedKindsJSON: '["chat"]', suggestedProtocol: "openai_chat_completions", suggestedProtocols: ["openai_chat_completions"], bindingCode: "", boundPlatformModels: [], upstreamModelStatus: "active", alreadySynced: false, alreadyBound: false, ...over };
}
const emptyPlan: SyncPlan = { addedModels: [], updatedModels: [], reactivatedModels: [], inactivatedModels: [], unchangedModels: [], protectedModels: [], unresolvedProtocolModels: [] };

describe("buildRows", () => {
  it("merges items of one upstream model and unions the protocols", () => {
    const rows = buildRows([item("m"), item("m", { suggestedProtocols: ["openai_responses"], suggestedProtocol: "openai_responses" })], emptyPlan);
    expect(rows).toHaveLength(1);
    expect(rows[0]?.protocols).toEqual(["openai_chat_completions", "openai_responses"]);
  });
  it("lists unbound models first", () => {
    const rows = buildRows([item("bound", { alreadyBound: true }), item("free")], emptyPlan);
    expect(rows.map((r) => r.upstreamModelName)).toEqual(["free", "bound"]);
  });
  it("adds rows for models the plan will inactivate", () => {
    const rows = buildRows([item("a")], { ...emptyPlan, inactivatedModels: ["gone", "a"] });
    expect(rows.map((r) => [r.upstreamModelName, r.vanished])).toEqual([["a", false], ["gone", true]]);
  });
});

describe("selection and search", () => {
  const rows = buildRows([item("a"), item("b", { alreadyBound: true }), item("c", { suggestedProtocols: [], suggestedProtocol: "" })], emptyPlan);
  it("preselects unbound models that have a suggested protocol", () => {
    expect([...defaultSelection(rows)]).toEqual(["a"]);
  });
  it("searches name, suggestion, protocol and status", () => {
    expect(searchRows(rows, "RESPONSES")).toEqual([]);
    expect(searchRows(rows, "chat_completions").map((r) => r.upstreamModelName)).toEqual(["a", "b"]);
    expect(searchRows(rows, "").length).toBe(3);
  });
});

describe("plan", () => {
  const plan: SyncPlan = { ...emptyPlan, addedModels: ["a"], inactivatedModels: ["z"], unchangedModels: ["b"] };
  it("counts each outcome and filters rows by chosen chips", () => {
    expect(outcomeCounts(plan)).toMatchObject({ addedModels: 1, inactivatedModels: 1, unchangedModels: 1, protectedModels: 0 });
    const rows = buildRows([item("a"), item("b")], plan);
    expect(rowsForOutcomes(rows, plan, new Set(["addedModels"] as const)).map((r) => r.upstreamModelName)).toEqual(["a"]);
    expect(rowsForOutcomes(rows, plan, new Set()).length).toBe(3);
  });
  it("detects changes that need the catalog or a confirmation", () => {
    expect(hasCatalogChanges(plan)).toBe(true);
    expect(hasCatalogChanges({ ...emptyPlan, unchangedModels: ["a"] })).toBe(false);
    expect(needsConfirmation(plan)).toBe(true);
    expect(needsConfirmation(emptyPlan)).toBe(false);
  });
  it("allows an empty remote list only when it really is empty", () => {
    expect(allowEmpty([])).toBe(true);
    expect(allowEmpty([1])).toBe(false);
  });
  it("is up to date when nothing would change and nothing is selected", () => {
    const base = { loading: false, importing: false, snapshotId: "s1", plan: emptyPlan, selected: 0 };
    expect(canApply(base)).toBe(false);
    expect(canApply({ ...base, selected: 1 })).toBe(true);
    expect(canApply({ ...base, plan, selected: 0 })).toBe(true);
    expect(canApply({ ...base, snapshotId: "", selected: 1 })).toBe(false);
    expect(canApply({ ...base, plan: null, selected: 1 })).toBe(false);
    expect(canApply({ ...base, loading: true, selected: 1 })).toBe(false);
  });
});

describe("importItems", () => {
  const synced: SyncResult["syncedModels"] = [{ upstreamModelName: "a", bindingCode: "", suggestedProtocol: "", kindsJSON: "", status: "active" }];
  const rows = buildRows([item("a"), item("b")], emptyPlan);
  it("imports only chosen models the sync confirmed", () => {
    const items = importItems(rows, new Set(["a", "b"]), {}, synced);
    expect(items.map((i) => i.upstreamModelName)).toEqual(["a"]);
  });
  it("uses the edited name, else the suggestion, else the upstream name", () => {
    expect(importItems(rows, new Set(["a"]), { a: " custom " }, synced)[0]?.platformModelName).toBe("custom");
    const plain = buildRows([item("a", { suggestedPlatformModelName: "" })], emptyPlan);
    expect(importItems(plain, new Set(["a"]), {}, synced)[0]?.platformModelName).toBe("a");
  });
  it("derives the kinds from the protocols", () => {
    const image = buildRows([item("a", { suggestedProtocols: ["google_image_generation"], suggestedProtocol: "google_image_generation" })], emptyPlan);
    expect(importItems(image, new Set(["a"]), {}, synced)[0]).toMatchObject({ protocols: ["google_image_generation"], kindsJSON: '["image_gen","image_edit"]' });
  });
});
