"use client";

import { useEffect, useState } from "react";
import { File as FileIcon, FileText, Film, Image as ImageIcon, Music } from "lucide-react";
import { Dialog, DialogContent, DialogDescription, DialogTitle } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { fetchProtectedFile, useImageSource } from "../../hooks/use-image-source";
import { formatBytes } from "../../lib/format";
import type { ChatAttachment } from "../../types";

function fileUrl(fileId: string): string {
  return `/api/v1/files/${encodeURIComponent(fileId)}/content`;
}

export function isVideoAttachment(a: ChatAttachment): boolean {
  return a.mimeType.startsWith("video/") || a.detectedMime.startsWith("video/") || a.fileCategory === "video";
}

function iconFor(a: ChatAttachment) {
  if (a.kind === "image") return ImageIcon;
  if (isVideoAttachment(a)) return Film;
  if (a.mimeType.startsWith("audio/") || a.fileCategory === "audio") return Music;
  if (a.mimeType.startsWith("text/") || a.fileCategory === "pdf" || a.fileCategory === "word") return FileText;
  return FileIcon;
}

function ImageThumb({ a, size }: { a: ChatAttachment; size: "sm" | "lg" }) {
  const t = useT();
  const { src, status } = useImageSource(a.previewUrl ?? fileUrl(a.fileId));
  const [open, setOpen] = useState(false);
  if (status === "error") {
    return (
      <span role="img" aria-label={`${a.fileName}: ${t("chatMsg.mediaUnavailable")}`} title={t("chatMsg.mediaUnavailable")} className={cn("flex items-center justify-center rounded-lg border bg-muted text-muted-foreground", size === "sm" ? "size-16" : "h-40 w-56")}>
        <ImageIcon className="size-5" aria-hidden />
      </span>
    );
  }
  return (
    <>
      <button
        type="button"
        onClick={() => setOpen(true)}
        aria-label={t("chatMsg.previewAttachment", { name: a.fileName })}
        className={cn("relative overflow-hidden rounded-lg border bg-muted outline-none focus-visible:ring-2 focus-visible:ring-ring", size === "sm" ? "size-16" : "max-h-72 max-w-[min(100%,24rem)]")}
      >
        {src ? (
          // eslint-disable-next-line @next/next/no-img-element -- user uploads loaded through the session
          <img src={src} alt={a.fileName} loading="lazy" className={cn("object-cover", size === "sm" ? "size-full" : "max-h-72 w-auto")} />
        ) : (
          <span className="rio-shimmer block size-16" />
        )}
      </button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-4xl">
          <DialogTitle className="sr-only">{a.fileName}</DialogTitle>
          <DialogDescription className="sr-only">{t("chatMsg.previewAttachment", { name: a.fileName })}</DialogDescription>
          {src ? (
            // eslint-disable-next-line @next/next/no-img-element -- see above
            <img src={src} alt={a.fileName} className="max-h-[80dvh] w-full rounded-lg object-contain" />
          ) : null}
        </DialogContent>
      </Dialog>
    </>
  );
}

export function VideoPlayer({ a }: { a: ChatAttachment }) {
  const t = useT();
  const [src, setSrc] = useState<string | null>(a.previewUrl ?? null);
  const [failed, setFailed] = useState(false);
  useEffect(() => {
    if (a.previewUrl) return;
    let live = true;
    setFailed(false);
    // Bytes of the file through the session; the object URL is cached and released by the cache.
    fetchProtectedFile(a.fileId).then(
      (u) => live && setSrc(u),
      () => live && setFailed(true),
    );
    return () => {
      live = false;
    };
  }, [a.fileId, a.previewUrl]);
  if (!src && failed) {
    return (
      <div role="alert" className="flex aspect-video w-full max-w-md items-center justify-center rounded-xl border bg-muted px-3 text-center text-sm text-muted-foreground">
        {t("chatMsg.mediaUnavailable")}
      </div>
    );
  }
  if (!src) return <div className="rio-shimmer aspect-video w-full max-w-md rounded-xl" role="status" aria-label={t("common.states.loading")} />;
  return <video src={src} controls preload="metadata" className="max-h-96 w-full max-w-md rounded-xl border bg-black" />;
}

export function AttachmentCards({ attachments, className }: { attachments: readonly ChatAttachment[]; className?: string }) {
  if (attachments.length === 0) return null;
  const images = attachments.filter((a) => a.kind === "image");
  const others = attachments.filter((a) => a.kind !== "image");
  return (
    <div className={cn("flex flex-wrap items-start gap-2", className)}>
      {images.map((a) => (
        <ImageThumb key={a.fileId} a={a} size="sm" />
      ))}
      {others.map((a) => {
        const Icon = iconFor(a);
        return (
          <div key={a.fileId} className="flex max-w-60 items-center gap-2 rounded-lg border bg-card px-2.5 py-1.5 text-xs" title={a.fileName}>
            <Icon className="size-4 shrink-0 text-muted-foreground" />
            <span className="min-w-0">
              <span className="block truncate font-medium">{a.fileName}</span>
              {a.fileSize > 0 ? <span className="text-muted-foreground">{formatBytes(a.fileSize)}</span> : null}
            </span>
          </div>
        );
      })}
    </div>
  );
}

export function GeneratedImages({ attachments }: { attachments: readonly ChatAttachment[] }) {
  const images = attachments.filter((a) => a.kind === "image");
  if (images.length === 0) return null;
  return (
    <div className="flex flex-wrap gap-2">
      {images.map((a) => (
        <ImageThumb key={a.fileId} a={a} size="lg" />
      ))}
    </div>
  );
}
