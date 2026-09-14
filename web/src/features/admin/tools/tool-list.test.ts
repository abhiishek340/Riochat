import { describe, expect, it } from "vitest";
import type { McpServer, McpTool } from "./api";
import { filterTools, mergeTools, sortServers, sortTools, withToolCounts } from "./tool-list";

function tool(id: number, over: Partial<McpTool> = {}): McpTool {
  return {
    id, serverID: 1, serverName: "s", name: `tool_${id}`, displayName: "", description: "", inputSchemaJSON: "{}", status: "active", sortOrder: 0, priceNanousd: 0,
    attachmentInputMode: "none", attachmentArgument: "", attachmentEncoding: "", attachmentPromptArgument: "", createdAt: "2026-01-01T00:00:00Z", updatedAt: "2026-01-01T00:00:00Z", ...over,
  };
}

describe("filterTools", () => {
  const tools = [tool(1, { displayName: "Web search", description: "find pages" }), tool(2, { status: "inactive", name: "fetch_url" })];
  it("matches name, display name and description", () => {
    expect(filterTools(tools, "SEARCH", "").map((t) => t.id)).toEqual([1]);
    expect(filterTools(tools, "fetch", "").map((t) => t.id)).toEqual([2]);
    expect(filterTools(tools, "pages", "").map((t) => t.id)).toEqual([1]);
  });
  it("filters by status", () => {
    expect(filterTools(tools, "", "inactive").map((t) => t.id)).toEqual([2]);
  });
});

describe("sortTools", () => {
  it("uses the display name, falling back to the name", () => {
    const tools = [tool(1, { displayName: "Zed" }), tool(2, { name: "alpha" }), tool(3, { displayName: "Mid" })];
    expect(sortTools(tools, "name_asc", "en").map((t) => t.id)).toEqual([2, 3, 1]);
    expect(sortTools(tools, "name_desc", "en").map((t) => t.id)).toEqual([1, 3, 2]);
  });
  it("custom order follows sortOrder then label then id", () => {
    const tools = [tool(1, { sortOrder: 2 }), tool(2, { sortOrder: 1, displayName: "b" }), tool(3, { sortOrder: 1, displayName: "a" })];
    expect(sortTools(tools, "custom", "en").map((t) => t.id)).toEqual([3, 2, 1]);
  });
  it("puts enabled tools first when sorting by status", () => {
    const tools = [tool(1, { status: "inactive" }), tool(2)];
    expect(sortTools(tools, "status", "en").map((t) => t.id)).toEqual([2, 1]);
  });
});

describe("counters", () => {
  const server = { id: 1, toolCount: 9, activeToolCount: 9, sortOrder: 0 } as McpServer;
  it("recomputes from the tool list", () => {
    const next = withToolCounts(server, [tool(1), tool(2, { status: "inactive" })]);
    expect([next.toolCount, next.activeToolCount]).toEqual([2, 1]);
  });
  it("merges updated tools in place", () => {
    const merged = mergeTools([tool(1), tool(2)], [tool(2, { status: "inactive" })]);
    expect(merged.map((t) => t.status)).toEqual(["active", "inactive"]);
  });
  it("orders servers by sortOrder then id", () => {
    const list = sortServers([{ id: 3, sortOrder: 1 }, { id: 2, sortOrder: 1 }, { id: 9, sortOrder: 0 }] as McpServer[]);
    expect(list.map((s) => s.id)).toEqual([9, 2, 3]);
  });
});
