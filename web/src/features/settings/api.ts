import { api } from "@/lib/api/client";
import type { AuthUser } from "@/lib/api/types";
import type { ProfileFields } from "./general/profile-draft";

export async function patchProfile(body: Partial<ProfileFields> & { appearancePreferences?: string }): Promise<AuthUser> {
  return (await api.patch<{ user: AuthUser }>("/me", body)).user;
}

export async function patchUsername(username: string): Promise<AuthUser> {
  return (await api.patch<{ user: AuthUser }>("/me/username", { username })).user;
}

/** Uploads an avatar image; resolves to the stored file id. */
export async function uploadAvatar(file: File): Promise<string> {
  const form = new FormData();
  form.append("purpose", "avatar");
  form.append("file", file);
  const data = await api.post<unknown>("/files", form);
  const id = findFileID(data);
  if (!id) throw new Error("errors.generic");
  return id;
}

function findFileID(data: unknown): string {
  if (typeof data !== "object" || data === null) return "";
  const rec = data as Record<string, unknown>;
  if (typeof rec.fileID === "string") return rec.fileID;
  if (typeof rec.id === "string" && rec.id.startsWith("file_")) return rec.id;
  for (const key of ["file", "upload", "item"]) {
    const nested = findFileID(rec[key]);
    if (nested) return nested;
  }
  return "";
}
