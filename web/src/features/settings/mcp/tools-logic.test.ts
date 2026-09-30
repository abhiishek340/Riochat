import { describe, expect, it } from "vitest";
import { groupByServer, matchesQuery, normalizeTools, pruneSelection, serializeToolIds, toggleTool, toolLabel } from "./tools-logic";

const tools = normalizeTools({
  results: [
    { id: 1, serverID: 10, serverName: "Search", name: "web_search", displayName: "Web search", description: "Look things up" },
    { id: 2, serverID: 10, serverName: "Search", name: "fetch", displayName: "", description: "" },
    { id: 3, serverID: 11, serverName: "Files", name: "read", displayName: "Read file", description: "" },
    { id: 0, name: "bad" },
    "junk",
  ],
});

describe("normalizeTools", () => {
  it("keeps valid tools and labels them", () => {
    expect(tools.map((t) => t.id)).toEqual([1, 2, 3]);
    expect(toolLabel(tools[0]!)).toBe("Web search");
    expect(toolLabel(tools[1]!)).toBe("fetch");
    expect(normalizeTools(null)).toEqual([]);
  });
  it("groups by server in first-seen order", () => {
    expect(groupByServer(tools).map((g) => [g.serverName, g.tools.length])).toEqual([["Search", 2], ["Files", 1]]);
  });
});

describe("selection", () => {
  it("toggles and refuses to pass the cap", () => {
    expect(toggleTool([1], 2, 3)).toEqual([1, 2]);
    expect(toggleTool([1, 2], 2, 3)).toEqual([1]);
    expect(toggleTool([1, 2, 3], 4, 3)).toEqual([1, 2, 3]);
    expect(toggleTool([1, 2, 3], 3, 3)).toEqual([1, 2]);
  });
  it("drops ids of tools that no longer exist", () => {
    expect(pruneSelection([1, 99, 3], tools)).toEqual([1, 3]);
  });
  it("stores a unique JSON array", () => {
    expect(serializeToolIds([2, 2, 5])).toBe("[2,5]");
  });
  it("searches names, descriptions and servers", () => {
    expect(matchesQuery(tools[0]!, "lookup")).toBe(false);
    expect(matchesQuery(tools[0]!, "look things")).toBe(true);
    expect(matchesQuery(tools[2]!, "FILES")).toBe(true);
    expect(matchesQuery(tools[2]!, "  ")).toBe(true);
  });
});
