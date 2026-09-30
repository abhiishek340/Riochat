import { api } from "@/lib/api/client";
import type { Stats } from "./rules";

export function loadStats(query: Record<string, string | number | undefined>, signal?: AbortSignal): Promise<Stats> {
  return api.get<Stats>("/admin/usage-statistics", { signal, query });
}
