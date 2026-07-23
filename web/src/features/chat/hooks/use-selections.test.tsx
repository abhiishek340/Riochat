// @vitest-environment jsdom
import { act, cleanup, renderHook, waitFor } from "@testing-library/react";
import type { ReactNode } from "react";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { I18nProvider } from "@/lib/i18n/provider";
import type { McpToolDTO, ProjectDTO, SkillSummaryDTO } from "../types";

const api = vi.hoisted(() => ({
  listSkills: vi.fn(),
  listKnowledgeBases: vi.fn(),
}));
vi.mock("../api", () => api);

import { useSelections } from "./use-selections";

const skill = (id: number): SkillSummaryDTO => ({ id, scope: "user", title: `Skill ${id}`, trigger: "", description: "", enabled: true, sortOrder: 0 });
const tool = (id: number): McpToolDTO => ({ id, serverID: 1, serverName: "srv", name: `t${id}`, displayName: `Tool ${id}`, description: "", attachmentInputMode: "", priceNanousd: 0, status: "active" });
const project: ProjectDTO = {
  publicID: "p1", name: "P", description: "", systemPrompt: "", defaultModel: "", mcpDefaultMode: "custom", defaultMCPToolIDs: [1], defaultSkillIDs: [7], defaultKnowledgeBaseIDs: [], color: "", icon: "", sortOrder: 0, status: "active",
};

const wrapper = ({ children }: { children: ReactNode }) => <I18nProvider>{children}</I18nProvider>;

beforeEach(() => {
  localStorage.clear();
  api.listSkills.mockReset();
  api.listKnowledgeBases.mockReset();
});
afterEach(cleanup);

describe("useSelections project defaults", () => {
  it("still applies the project's skills when the tool list is refreshed while they load", async () => {
    let release!: (v: unknown) => void;
    api.listSkills.mockImplementation(() => new Promise((r) => (release = r)));
    const base = { key: "draft", userId: "u1", project, newChatRevision: 1, toolsLoaded: true, cap: 32, userDefaultToolIds: [] as number[], kbEnabled: false, authed: true };
    const { result, rerender } = renderHook((props: { tools: McpToolDTO[] }) => useSelections({ ...base, tools: props.tools }), { wrapper, initialProps: { tools: [tool(1), tool(2)] } });
    await waitFor(() => expect(result.current.toolIds).toEqual([1]));
    // The tool list is fetched again (new array) before the skills answer arrives.
    rerender({ tools: [tool(1), tool(2)] });
    await act(async () => {
      release({ total: 1, results: [skill(7)] });
    });
    await waitFor(() => expect(result.current.skills.map((s) => s.id)).toEqual([7]));
  });

  it("does not overwrite what the user chose in the meantime", async () => {
    let release!: (v: unknown) => void;
    api.listSkills.mockImplementation(() => new Promise((r) => (release = r)));
    const tools = [tool(1), tool(2)];
    const base = { key: "draft", userId: "u1", project, newChatRevision: 1, tools, toolsLoaded: true, cap: 32, userDefaultToolIds: [] as number[], kbEnabled: false, authed: true };
    const { result } = renderHook(() => useSelections(base), { wrapper });
    await waitFor(() => expect(result.current.toolIds).toEqual([1]));
    act(() => {
      result.current.toggleTool(tools[1] as McpToolDTO);
    });
    await act(async () => {
      release({ total: 1, results: [skill(7)] });
    });
    expect(result.current.toolIds).toEqual([1, 2]);
    expect(result.current.skills).toEqual([]);
  });
});

describe("useSelections knowledge base pruning", () => {
  it("drops selected knowledge bases that are not ready any more, once", async () => {
    api.listKnowledgeBases.mockResolvedValue({
      total: 2,
      results: [
        { publicID: "kb1", name: "A", readyFileCount: 3 },
        { publicID: "kb2", name: "B", readyFileCount: 0 },
      ],
    });
    localStorage.setItem("riochat:selections:u1", JSON.stringify({ c1: { toolIds: [], skills: [], knowledgeBaseIds: ["kb1", "kb2", "gone"], updatedAt: Date.now() } }));
    const args = { key: "c1", userId: "u1", project: null, newChatRevision: 0, tools: [], toolsLoaded: true, cap: 32, userDefaultToolIds: [] as number[], kbEnabled: true, authed: true };
    const { result } = renderHook(() => useSelections(args), { wrapper });
    await waitFor(() => expect(result.current.kbIds).toEqual(["kb1"]));
    expect(api.listKnowledgeBases).toHaveBeenCalledTimes(1);
  });

  it("leaves the selection alone when the check cannot run", async () => {
    api.listKnowledgeBases.mockRejectedValue(new Error("offline"));
    localStorage.setItem("riochat:selections:u1", JSON.stringify({ c1: { toolIds: [], skills: [], knowledgeBaseIds: ["kb1"], updatedAt: Date.now() } }));
    const args = { key: "c1", userId: "u1", project: null, newChatRevision: 0, tools: [], toolsLoaded: true, cap: 32, userDefaultToolIds: [] as number[], kbEnabled: true, authed: true };
    const { result } = renderHook(() => useSelections(args), { wrapper });
    await waitFor(() => expect(api.listKnowledgeBases).toHaveBeenCalled());
    expect(result.current.kbIds).toEqual(["kb1"]);
  });
});
