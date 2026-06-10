import { api } from "@/lib/api/client";
import { isRecord, str } from "@/lib/utils/guards";

export interface ChatModelOption {
  name: string;
  group: string;
}

/** Public models that can chat, in the order the server groups them. */
export async function listChatModels(signal?: AbortSignal): Promise<ChatModelOption[]> {
  const data = await api.get<unknown>("/models", { signal });
  if (!Array.isArray(data)) return [];
  const out: ChatModelOption[] = [];
  for (const item of data) {
    if (!isRecord(item)) continue;
    const name = str(item.platformModelName).trim();
    if (!name) continue;
    if (!kindsInclude(item.kindsJSON, "chat")) continue;
    out.push({ name, group: str(item.displayGroupName) || str(item.vendorName) });
  }
  return out;
}

function kindsInclude(raw: unknown, kind: string): boolean {
  if (typeof raw !== "string" || raw.trim() === "") return true;
  try {
    const parsed: unknown = JSON.parse(raw);
    return Array.isArray(parsed) ? parsed.includes(kind) : true;
  } catch {
    return true;
  }
}

export async function fetchBillingMode(signal?: AbortSignal): Promise<string> {
  const data = await api.get<unknown>("/billing/config", { signal });
  return isRecord(data) ? str(data.mode, "self") : "self";
}

export async function fetchCompactPolicy(signal?: AbortSignal): Promise<boolean> {
  const data = await api.get<unknown>("/settings/chat-context-policy", { signal });
  return isRecord(data) && data.contextCompactEnabled === true;
}

/** Groups options by their group name, keeping first-seen order. */
export function groupModels(options: readonly ChatModelOption[]): { group: string; items: ChatModelOption[] }[] {
  const order: string[] = [];
  const map = new Map<string, ChatModelOption[]>();
  for (const o of options) {
    if (!map.has(o.group)) {
      map.set(o.group, []);
      order.push(o.group);
    }
    map.get(o.group)?.push(o);
  }
  return order.map((group) => ({ group, items: map.get(group) ?? [] }));
}
