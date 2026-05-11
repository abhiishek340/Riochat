import { api } from "@/lib/api/client";
import { readAnnouncements, type Announcement } from "../lib/announcements-model";

export async function loadAnnouncements(includeDismissed: boolean, signal?: AbortSignal): Promise<Announcement[]> {
  const data = await api.get<unknown>("/announcements", { signal, query: { include_dismissed: includeDismissed ? "true" : undefined } });
  return readAnnouncements(data);
}

/** The item's own `updatedAt` is echoed so a later edit of the announcement resurfaces it. */
export async function dismissForToday(item: Pick<Announcement, "id" | "updatedAt">): Promise<void> {
  await api.post(`/announcements/${item.id}/dismiss-today`, { updatedAt: item.updatedAt });
}

export async function closeAnnouncement(item: Pick<Announcement, "id" | "updatedAt">): Promise<void> {
  await api.post(`/announcements/${item.id}/close`, { updatedAt: item.updatedAt });
}

/** Runs one request per item in parallel; reports which ones went through and how many did not. */
export async function applyToAll(
  items: readonly Announcement[],
  action: (item: Announcement) => Promise<void>,
): Promise<{ failed: number; succeeded: Announcement[] }> {
  const results = await Promise.allSettled(items.map((item) => action(item)));
  return {
    failed: results.filter((r) => r.status === "rejected").length,
    succeeded: items.filter((_, index) => results[index]?.status === "fulfilled"),
  };
}
