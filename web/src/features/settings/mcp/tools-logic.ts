/** Default MCP tool selection: grouping, the per-message cap and the stored form. */
import { isRecord, num, str } from "@/lib/utils/guards";

export interface McpTool {
  id: number;
  serverID: number;
  serverName: string;
  name: string;
  displayName: string;
  description: string;
}

export const DEFAULT_MAX_TOOLS = 32;

export function normalizeTools(raw: unknown): McpTool[] {
  const list = isRecord(raw) && Array.isArray(raw.results) ? raw.results : [];
  return list.flatMap((item): McpTool[] => {
    if (!isRecord(item)) return [];
    const id = num(item.id);
    if (id <= 0) return [];
    return [
      {
        id,
        serverID: num(item.serverID),
        serverName: str(item.serverName),
        name: str(item.name),
        displayName: str(item.displayName),
        description: str(item.description),
      },
    ];
  });
}

export function toolLabel(tool: Pick<McpTool, "displayName" | "name">): string {
  return tool.displayName.trim() || tool.name;
}

export function groupByServer(tools: readonly McpTool[]): { serverID: number; serverName: string; tools: McpTool[] }[] {
  const groups = new Map<number, { serverID: number; serverName: string; tools: McpTool[] }>();
  for (const tool of tools) {
    const group = groups.get(tool.serverID) ?? { serverID: tool.serverID, serverName: tool.serverName, tools: [] };
    group.tools.push(tool);
    groups.set(tool.serverID, group);
  }
  return [...groups.values()];
}

/** Keeps stored ids that still exist (tools can be removed by the administrator). */
export function pruneSelection(selected: readonly number[], tools: readonly McpTool[]): number[] {
  const known = new Set(tools.map((t) => t.id));
  return selected.filter((id) => known.has(id));
}

/** Toggling beyond the cap is refused (returns the list unchanged). */
export function toggleTool(selected: readonly number[], id: number, max: number): number[] {
  if (selected.includes(id)) return selected.filter((x) => x !== id);
  if (selected.length >= max) return [...selected];
  return [...selected, id];
}

export function serializeToolIds(ids: readonly number[]): string {
  return JSON.stringify([...new Set(ids)]);
}

export function matchesQuery(tool: McpTool, query: string): boolean {
  const q = query.trim().toLowerCase();
  if (!q) return true;
  return [tool.name, tool.displayName, tool.description, tool.serverName].some((s) => s.toLowerCase().includes(q));
}
