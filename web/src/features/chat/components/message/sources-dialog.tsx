"use client";

import { useState } from "react";
import { BookOpen, ImageIcon } from "lucide-react";
import { Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { distinctSourceCount } from "../../lib/trace";
import type { KnowledgeSource } from "../../types";

export function SourcesButton({ sources }: { sources: readonly KnowledgeSource[] }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  if (sources.length === 0) return null;
  const files = distinctSourceCount(sources);
  return (
    <>
      <button type="button" onClick={() => setOpen(true)} className="mt-1 inline-flex items-center gap-1.5 rounded-full border bg-card px-2.5 py-1 text-xs text-muted-foreground hover:text-foreground" data-capture-hide>
        <BookOpen className="size-3.5" />
        {t("chatMsg.sources.button", { count: files })}
      </button>
      <Dialog open={open} onOpenChange={setOpen}>
        <DialogContent className="max-w-2xl">
          <DialogHeader>
            <DialogTitle>{t("chatMsg.sources.title", { count: files })}</DialogTitle>
            <DialogDescription>{t("chatMsg.sources.description")}</DialogDescription>
          </DialogHeader>
          <ul className="grid max-h-[60dvh] gap-2 overflow-auto">
            {sources.map((s, i) => (
              <li key={`${s.fileId}-${s.chunkIndex}-${i}`} className="grid gap-1 rounded-lg border p-2.5 text-sm">
                <div className="flex items-center gap-2 text-xs">
                  {s.modality === "image" ? <ImageIcon className="size-3.5 text-muted-foreground" aria-label={t("chatMsg.sources.imageMatch")} /> : null}
                  <span className="min-w-0 flex-1 truncate font-medium">{s.fileName || s.fileId}</span>
                  <span className="shrink-0 text-muted-foreground">
                    #{s.chunkIndex + 1} · {Math.round(s.score * 100)}%
                  </span>
                </div>
                {s.preview ? <p className="whitespace-pre-wrap text-muted-foreground">{s.preview}</p> : null}
              </li>
            ))}
          </ul>
        </DialogContent>
      </Dialog>
    </>
  );
}
