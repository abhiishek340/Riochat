import { api } from "@/lib/api/client";
import { seg } from "@/lib/api/http";
import { readPage, type PageResult } from "../lib/paging";
import type { Announcement } from "./rules";

export interface AnnouncementQuery {
  page: number;
  pageSize: number;
  q: string;
  status: string;
  type: string;
  pinned: string;
}

export async function listAnnouncements(query: AnnouncementQuery, signal?: AbortSignal): Promise<PageResult<Announcement>> {
  const raw = await api.get<unknown>("/admin/announcements", {
    signal,
    query: { page: query.page, page_size: query.pageSize, q: query.q.trim(), status: query.status, type: query.type, pinned: query.pinned },
  });
  return readPage<Announcement>(raw);
}

export interface AnnouncementBody {
  title: string;
  contentMarkdown: string;
  status: string;
  type: string;
  pinned: boolean;
  priority: number;
  startsAt: string | null;
  expiresAt: string | null;
}

export async function createAnnouncement(body: AnnouncementBody): Promise<Announcement> {
  const out = await api.post<{ announcement: Announcement }>("/admin/announcements", body);
  return out.announcement;
}

export async function updateAnnouncement(id: number, body: Partial<AnnouncementBody>): Promise<Announcement> {
  const out = await api.patch<{ announcement: Announcement }>(`/admin/announcements/${seg(id)}`, body);
  return out.announcement;
}

export async function deleteAnnouncement(id: number): Promise<void> {
  await api.delete(`/admin/announcements/${seg(id)}`);
}
