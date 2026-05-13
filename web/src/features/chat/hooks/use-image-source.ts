"use client";

import { useEffect, useState } from "react";
import { api } from "@/lib/api/client";
import { absolutizeApiPath } from "@/lib/api/base-url";

const PROTECTED = /^(?:https?:\/\/[^/]+)?\/api\/v1\/files\/([^/?#]+)\/content(?:[?#].*)?$/;

/** File id of a protected image reference (`/api/v1/files/<id>/content`), else null. */
export function protectedFileId(ref: string): string | null {
  const m = PROTECTED.exec(ref.trim());
  return m ? decodeURIComponent(m[1] as string) : null;
}

const CACHE_MAX = 60;
const cache = new Map<string, Promise<string>>();

/** Fetches a protected file with the session token and returns an object URL (cached). */
export function fetchProtectedFile(fileId: string): Promise<string> {
  const hit = cache.get(fileId);
  if (hit) return hit;
  const p = api
    .raw(`/files/${encodeURIComponent(fileId)}/content`)
    .then((r) => r.blob())
    .then((b) => URL.createObjectURL(b));
  p.catch(() => cache.delete(fileId));
  cache.set(fileId, p);
  if (cache.size > CACHE_MAX) {
    const oldest = cache.keys().next().value as string;
    const old = cache.get(oldest);
    cache.delete(oldest);
    void old?.then((u) => URL.revokeObjectURL(u));
  }
  return p;
}

export interface ImageSource {
  src: string | null;
  status: "loading" | "ready" | "error";
}

/** Resolves a model-provided image reference to something an <img> can load. */
export function useImageSource(ref: string): ImageSource {
  const id = protectedFileId(ref);
  const [state, setState] = useState<{ ref: string; src: string | null; status: ImageSource["status"] }>({ ref, src: null, status: "loading" });
  useEffect(() => {
    if (id === null) return;
    let live = true;
    fetchProtectedFile(id).then(
      (src) => live && setState({ ref, src, status: "ready" }),
      () => live && setState({ ref, src: null, status: "error" }),
    );
    return () => {
      live = false;
    };
  }, [id, ref]);
  if (id === null) return { src: ref ? absolutizeApiPath(ref) : null, status: ref ? "ready" : "error" };
  // Results are tied to the reference they were loaded for.
  return state.ref === ref ? { src: state.src, status: state.status } : { src: null, status: "loading" };
}

/** File name for downloading an image reference. */
export function imageDownloadName(ref: string, alt: string): string {
  let last = "";
  try {
    last = decodeURIComponent(new URL(ref, "http://x").pathname.split("/").filter(Boolean).pop() ?? "");
  } catch {
    last = "";
  }
  last = last.replace(/[\\/:*?"<>|]+/g, "-");
  if (/\.[A-Za-z0-9]{2,5}$/.test(last) && !/^content$/.test(last)) return last;
  const base = (alt || "image").replace(/[\\/:*?"<>|]+/g, "-").trim() || "image";
  return `${base}.png`;
}
