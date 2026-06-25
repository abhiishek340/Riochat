import { api, publicApi } from "@/lib/api/client";
import { isRecord, str } from "@/lib/utils/guards";
import { readSnapshot, type ShareSnapshot } from "../lib/share-model";

export async function fetchSharedConversation(shareId: string, signal?: AbortSignal): Promise<ShareSnapshot> {
  const data = await publicApi.get<unknown>(`/shared-conversations/${encodeURIComponent(shareId)}`, { signal });
  const snapshot = readSnapshot(data, shareId);
  if (!snapshot) throw new Error("errors.resource.notFound");
  return snapshot;
}

/** Copies the snapshot into the signed-in viewer's own account; resolves to the new conversation id. */
export async function cloneSharedConversation(shareId: string): Promise<string> {
  const data = await api.post<unknown>(`/shared-conversations/${encodeURIComponent(shareId)}/clone`);
  const id = isRecord(data) ? str(data.publicID) : "";
  if (!id) throw new Error("errors.generic");
  return id;
}
