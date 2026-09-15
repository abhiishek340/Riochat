import type { McpServer, McpTool } from "./api";

export type ToolSort = "custom" | "name_asc" | "name_desc" | "status" | "updated_desc" | "updated_asc";
export const TOOL_SORTS: readonly ToolSort[] = ["custom", "name_asc", "name_desc", "status", "updated_desc", "updated_asc"];

export function toolLabel(tool: Pick<McpTool, "displayName" | "name">): string {
  return tool.displayName.trim() || tool.name;
}

export function filterTools(tools: readonly McpTool[], query: string, status: "" | "active" | "inactive"): McpTool[] {
  const q = query.trim().toLowerCase();
  return tools.filter((tool) => {
    if (status && tool.status !== status) return false;
    if (!q) return true;
    return [tool.name, tool.displayName, tool.description].some((text) => text.toLowerCase().includes(q));
  });
}

/** Custom order is the admin-defined one: sort order, then label, then id. */
export function sortTools(tools: readonly McpTool[], sort: ToolSort, locale: string): McpTool[] {
  const byLabel = (a: McpTool, b: McpTool) => toolLabel(a).localeCompare(toolLabel(b), locale);
  const list = [...tools];
  switch (sort) {
    case "name_asc":
      return list.sort((a, b) => byLabel(a, b) || a.id - b.id);
    case "name_desc":
      return list.sort((a, b) => byLabel(b, a) || a.id - b.id);
    case "status":
      return list.sort((a, b) => Number(b.status === "active") - Number(a.status === "active") || byLabel(a, b) || a.id - b.id);
    case "updated_desc":
      return list.sort((a, b) => Date.parse(b.updatedAt) - Date.parse(a.updatedAt) || a.id - b.id);
    case "updated_asc":
      return list.sort((a, b) => Date.parse(a.updatedAt) - Date.parse(b.updatedAt) || a.id - b.id);
    default:
      return list.sort((a, b) => a.sortOrder - b.sortOrder || byLabel(a, b) || a.id - b.id);
  }
}

export function sortServers(servers: readonly McpServer[]): McpServer[] {
  return [...servers].sort((a, b) => a.sortOrder - b.sortOrder || a.id - b.id);
}

/** The server's counters follow its tool list after every local change. */
export function withToolCounts(server: McpServer, tools: readonly McpTool[]): McpServer {
  return { ...server, toolCount: tools.length, activeToolCount: tools.filter((t) => t.status === "active").length };
}

/** Replaces tools by id, keeping the order of `tools`. */
export function mergeTools(tools: readonly McpTool[], updated: readonly McpTool[]): McpTool[] {
  const by = new Map(updated.map((t) => [t.id, t]));
  return tools.map((t) => by.get(t.id) ?? t);
}
