import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";

export interface McpServer {
  id: number;
  name: string;
  baseURL: string;
  headersJSON: string;
  status: "active" | "inactive" | (string & {});
  sortOrder: number;
  toolCount: number;
  activeToolCount: number;
  lastSyncedAt: string | null;
  lastError: string;
  requiresToolMetadataSyncConfirmation: boolean;
  createdAt: string;
  updatedAt: string;
}

export interface McpTool {
  id: number;
  serverID: number;
  serverName: string;
  name: string;
  displayName: string;
  description: string;
  inputSchemaJSON: string;
  status: "active" | "inactive" | (string & {});
  sortOrder: number;
  priceNanousd: number;
  attachmentInputMode: "none" | "image" | (string & {});
  attachmentArgument: string;
  attachmentEncoding: "" | "base64" | "data_url" | (string & {});
  attachmentPromptArgument: string;
  createdAt: string;
  updatedAt: string;
}

export interface ServerInput {
  name: string;
  baseURL: string;
  authToken?: string;
  headersJSON: string;
  status: string;
}

export async function listServers(signal?: AbortSignal): Promise<McpServer[]> {
  const out = await api.get<{ results?: McpServer[] }>("/admin/mcp/servers", { signal });
  return out?.results ?? [];
}

export async function createServer(input: ServerInput): Promise<McpServer> {
  const out = await api.post<{ server: McpServer }>("/admin/mcp/servers", input);
  return out.server;
}

export async function updateServer(id: number, input: ServerInput): Promise<McpServer> {
  const out = await api.patch<{ server: McpServer }>(`/admin/mcp/servers/${seg(id)}`, input);
  return out.server;
}

export async function deleteServer(id: number): Promise<void> {
  await api.delete(`/admin/mcp/servers/${seg(id)}`);
}

export async function listServerTools(serverId: number, signal?: AbortSignal): Promise<McpTool[]> {
  const out = await api.get<{ results?: McpTool[] }>(`/admin/mcp/servers/${seg(serverId)}/tools`, { signal });
  return out?.results ?? [];
}

export async function syncServer(serverId: number, overwriteCustomized: boolean): Promise<McpTool[]> {
  const out = await api.post<{ results?: McpTool[] }>(`/admin/mcp/servers/${seg(serverId)}/sync`, undefined, {
    query: overwriteCustomized ? { overwrite_customized_metadata: true } : undefined,
  });
  return out?.results ?? [];
}

export async function setToolsStatus(serverId: number, toolIds: number[], status: string): Promise<McpTool[]> {
  const out = await api.patch<{ results?: McpTool[] }>(`/admin/mcp/servers/${seg(serverId)}/tools/status`, { status, toolIDs: toolIds });
  return out?.results ?? [];
}

export type ToolPatch = Partial<Pick<McpTool, "displayName" | "description" | "attachmentInputMode" | "attachmentArgument" | "attachmentEncoding" | "attachmentPromptArgument" | "priceNanousd" | "status">>;

export async function patchTool(id: number, patch: ToolPatch): Promise<McpTool> {
  return api.patch<McpTool>(`/admin/mcp/tools/${seg(id)}`, patch);
}

export interface OrderResult {
  server: McpServer;
  tools: McpTool[];
}

export async function saveOrder(servers: Array<{ serverID: number; toolIDs: number[] }>): Promise<OrderResult[]> {
  const out = await api.patch<{ results?: OrderResult[] }>("/admin/mcp/servers/order", { servers });
  return out?.results ?? [];
}
