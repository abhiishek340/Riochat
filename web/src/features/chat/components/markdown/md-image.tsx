"use client";

import { memo, useState } from "react";
import { Download, Eye, Pencil } from "lucide-react";
import { Dialog, DialogContent, DialogDescription, DialogTitle, Tooltip, toast } from "@/components/ui";
import { api } from "@/lib/api/client";
import { absolutizeApiPath } from "@/lib/api/base-url";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { imageDownloadName, protectedFileId, useImageSource } from "../../hooks/use-image-source";
import { downloadBlob } from "../../lib/clipboard";
import { useMarkdownContext } from "./context";

export const MdImage = memo(function MdImage({ src, alt, inLink = false }: { src: string; alt: string; inLink?: boolean }) {
  const t = useT();
  const ctx = useMarkdownContext();
  const { src: resolved, status } = useImageSource(src);
  const [loaded, setLoaded] = useState(false);
  const [failed, setFailed] = useState(false);
  const [open, setOpen] = useState(false);
  const canEdit = !!ctx.editImage && !!ctx.imageEditable?.(src);

  if (status === "error" || failed) {
    return <span className="inline-block rounded-md bg-muted px-2 py-1 text-sm text-muted-foreground">{alt || t("chatRich.md.imageUnavailable")}</span>;
  }

  const download = async () => {
    try {
      const id = protectedFileId(src);
      const blob = id ? await (await api.raw(`/files/${encodeURIComponent(id)}/content`)).blob() : await (await fetch(absolutizeApiPath(src))).blob();
      downloadBlob(blob, imageDownloadName(src, alt));
    } catch {
      if (!protectedFileId(src)) window.open(absolutizeApiPath(src), "_blank", "noopener,noreferrer");
      else toast.error(t("chatRich.md.imageDownloadFailed"));
    }
  };

  return (
    <span className="group/img relative my-2 inline-block max-w-full align-top">
      {!loaded ? <span className="rio-shimmer block h-40 w-64 max-w-full rounded-lg" aria-hidden /> : null}
      {resolved ? (
        // eslint-disable-next-line @next/next/no-img-element -- model output with dynamic sources, loaded through the session
        <img
          src={resolved}
          alt={alt}
          loading="lazy"
          referrerPolicy="no-referrer"
          onLoad={() => setLoaded(true)}
          onError={() => setFailed(true)}
          className={cn("max-h-[28rem] max-w-full rounded-lg border object-contain", !loaded && "absolute inset-0 opacity-0")}
        />
      ) : null}
      {loaded && !inLink ? (
        <span className="absolute right-2 top-2 flex gap-1 rounded-lg bg-background/85 p-1 opacity-0 shadow-sm backdrop-blur transition-opacity group-hover/img:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100" data-capture-hide>
          <Tooltip label={t("chatRich.md.imagePreview")}>
            <button type="button" onClick={() => setOpen(true)} aria-label={t("chatRich.md.imagePreview")} className="rounded p-1 hover:bg-accent">
              <Eye className="size-4" />
            </button>
          </Tooltip>
          {canEdit ? (
            <Tooltip label={t("chatRich.md.imageEdit")}>
              <button type="button" onClick={() => ctx.editImage?.(src, alt)} aria-label={t("chatRich.md.imageEdit")} className="rounded p-1 hover:bg-accent">
                <Pencil className="size-4" />
              </button>
            </Tooltip>
          ) : null}
          <Tooltip label={t("chatRich.md.imageDownload")}>
            <button type="button" onClick={() => void download()} aria-label={t("chatRich.md.imageDownload")} className="rounded p-1 hover:bg-accent">
              <Download className="size-4" />
            </button>
          </Tooltip>
        </span>
      ) : null}
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-4xl">
          <DialogTitle className="sr-only">{t("chatRich.md.imagePreview")}</DialogTitle>
          <DialogDescription className="sr-only">{alt}</DialogDescription>
          {resolved ? (
            // eslint-disable-next-line @next/next/no-img-element -- see above
            <img src={resolved} alt={alt} referrerPolicy="no-referrer" className="max-h-[80dvh] w-full rounded-lg object-contain" />
          ) : null}
        </DialogContent>
      </Dialog>
    </span>
  );
});
