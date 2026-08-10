// @vitest-environment jsdom
import { cleanup, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { clearToasts, renderWithApp } from "../test-utils";
import type { PlatformModel } from "../lib/wire";

const llm = vi.hoisted(() => ({
  createModel: vi.fn(),
  updateModel: vi.fn(),
  bindSource: vi.fn(),
  putModelGroups: vi.fn(),
  getModelGroups: vi.fn(),
  resolveCatalog: vi.fn(),
  listAllUpstreams: vi.fn(),
}));
vi.mock("../llm-api", async (orig) => ({ ...(await orig<object>()), ...llm }));
vi.mock("./api", async (orig) => ({
  ...(await orig<object>()),
  loadFallbackWindow: vi.fn().mockResolvedValue(128000),
  touchCatalog: vi.fn().mockResolvedValue(null),
  loadNativeToolCatalog: vi.fn().mockResolvedValue([]),
  loadUpstreamModels: vi.fn().mockResolvedValue([]),
}));
vi.mock("../billing/api", async (orig) => ({ ...(await orig<object>()), loadOfficial: vi.fn().mockResolvedValue({ items: [], fetchedAt: "", origin: "builtin", cached: true, stale: false }) }));
vi.mock("../lib/reference", async (orig) => ({ ...(await orig<object>()), loadPermissionGroups: vi.fn().mockResolvedValue([{ id: 7, name: "Staff" }]), loadAllModels: vi.fn().mockResolvedValue([]) }));

import { ModelSheet } from "./model-sheet";

const model = (over: Partial<PlatformModel> = {}): PlatformModel =>
  ({ id: 5, platformModelName: "gpt-5", vendor: "openai", vendorName: "OpenAI", vendorIcon: "", displayGroupID: null, displayGroupName: "", displayGroupIcon: "", kindsJSON: '["chat"]', icon: "", capabilitiesJSON: "", contextWindow: 0, systemPrompt: "", accessScope: "public", status: "active", description: "", cbPolicyMode: "default", cbFailureThreshold: 0, cbDurationMin: 0, cbWindowMin: 0, sortOrder: 0, sourceCount: 0, activeSourceCount: 0, protocolsJSON: "[]", upstreamNamesJSON: "[]", createdAt: "", updatedAt: "", ...over }) as PlatformModel;

beforeEach(() => {
  llm.createModel.mockReset().mockResolvedValue(model({ id: 9 }));
  llm.updateModel.mockReset().mockImplementation(async (_id: number, body: Record<string, unknown>) => model({ ...(body as object) }));
  llm.bindSource.mockReset().mockResolvedValue({});
  llm.putModelGroups.mockReset().mockResolvedValue({});
  llm.getModelGroups.mockReset().mockResolvedValue({ manualGroupIDs: [7], matchedGroupIDs: [], effectiveGroupIDs: [7], unassigned: false });
  llm.resolveCatalog.mockReset().mockResolvedValue({ matched: false, provider: null, modelId: null, inputModalities: [], outputModalities: [], contextWindow: null, reasoning: null, reasoningTemplate: null });
  llm.listAllUpstreams.mockReset().mockResolvedValue([]);
  clearToasts();
});
afterEach(cleanup);

function open(target: PlatformModel | "new", onSaved = vi.fn()) {
  renderWithApp(<ModelSheet target={target} onOpenChange={() => undefined} vendors={[{ id: 1, key: "openai", name: "OpenAI", icon: "", builtIn: true, sortOrder: 0 }]} groups={[]} onSaved={onSaved} onManageSources={() => undefined} onManagePresentation={() => undefined} />);
  return onSaved;
}

describe("ModelSheet", () => {
  it("refuses a name shorter than two characters without calling the server", async () => {
    const user = userEvent.setup();
    open("new");
    await user.type(await screen.findByRole("textbox", { name: /Model name/ }), "x");
    await user.click(screen.getByRole("button", { name: "Create" }));
    expect(await screen.findByRole("alert")).toBeTruthy();
    expect(llm.createModel).not.toHaveBeenCalled();
  });

  it("creates a model with the form values and no sources", async () => {
    const user = userEvent.setup();
    const onSaved = open("new");
    await user.type(await screen.findByRole("textbox", { name: /Model name/ }), "  my-model ");
    await user.click(screen.getByRole("button", { name: "Create" }));
    await waitFor(() => expect(llm.createModel).toHaveBeenCalledTimes(1));
    expect(llm.createModel.mock.calls[0]?.[0]).toMatchObject({ platformModelName: "my-model", vendor: "unknown", accessScope: "public", status: "active", capabilitiesJSON: "", cbFailureThreshold: 0 });
    expect("kindsJSON" in (llm.createModel.mock.calls[0]?.[0] as object)).toBe(false);
    expect(llm.bindSource).not.toHaveBeenCalled();
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
  });

  it("saves an edit with the full form and leaves the permission groups alone when unchanged", async () => {
    const user = userEvent.setup();
    const onSaved = open(model({ description: "old" }));
    const description = await screen.findByRole("textbox", { name: "Description" });
    await user.clear(description);
    await user.type(description, "new text");
    await user.click(screen.getByRole("button", { name: "Save" }));
    await waitFor(() => expect(llm.updateModel).toHaveBeenCalledTimes(1));
    expect(llm.updateModel.mock.calls[0]?.[0]).toBe(5);
    expect(llm.updateModel.mock.calls[0]?.[1]).toMatchObject({ platformModelName: "gpt-5", description: "new text", kindsJSON: '["chat"]', displayGroupID: 0 });
    expect(llm.putModelGroups).not.toHaveBeenCalled();
    await waitFor(() => expect(onSaved).toHaveBeenCalled());
  });
});
