"use client";

import { useEffect, useRef, useState } from "react";
import { Download, Copy, X } from "lucide-react";
import { Button, Dialog, DialogContent, DialogDescription, DialogTitle, toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { downloadBlob } from "../lib/clipboard";
import { screenshotFileName } from "../lib/screenshot";
import type { ScreenshotResult } from "../hooks/use-screenshot";

export function SelectionBar({
  count,
  busy,
  onSelectAll,
  onSelectLatest,
  onClear,
  onCapture,
  onExit,
}: {
  count: number;
  busy: boolean;
  onSelectAll: () => void;
  onSelectLatest: () => void;
  onClear: () => void;
  onCapture: () => void;
  onExit: () => void;
}) {
  const t = useT();
  return (
    <div className="mx-auto mb-2 flex w-full max-w-3xl flex-wrap items-center gap-2 rounded-xl border bg-card px-3 py-2 text-sm shadow-sm" role="toolbar" aria-label={t("chatNav.screenshot.select")} data-selection-bar>
      <span className="mr-auto text-muted-foreground" aria-live="polite">
        {t("chatNav.screenshot.selected", { count })}
      </span>
      <Button variant="ghost" size="sm" onClick={onSelectAll}>
        {t("chatNav.screenshot.selectAll")}
      </Button>
      <Button variant="ghost" size="sm" onClick={onSelectLatest}>
        {t("chatNav.screenshot.selectLatest")}
      </Button>
      <Button variant="ghost" size="sm" onClick={onClear} disabled={count === 0}>
        {t("chatNav.screenshot.clear")}
      </Button>
      <Button size="sm" onClick={onCapture} disabled={busy || count === 0}>
        {t("chatNav.screenshot.capture", { count })}
      </Button>
      <Button variant="outline" size="sm" onClick={onExit}>
        {t("chatNav.screenshot.exit")}
      </Button>
    </div>
  );
}

/** The dialog plays its exit for this long before the picture is released. */
export const EXIT_DELAY_MS = 220;

export function ScreenshotPreview({ result, title, onClose }: { result: ScreenshotResult | null; title: string; onClose: () => void }) {
  const t = useT();
  const [closing, setClosing] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );
  const close = () => {
    if (closing) return;
    setClosing(true);
    timer.current = setTimeout(() => {
      timer.current = null;
      setClosing(false);
      onClose();
    }, EXIT_DELAY_MS);
  };
  const copy = async () => {
    if (!result) return;
    if (typeof ClipboardItem === "undefined" || !navigator.clipboard?.write) {
      toast.error(t("chatNav.screenshot.copyUnsupported"));
      return;
    }
    try {
      await navigator.clipboard.write([new ClipboardItem({ "image/png": result.blob })]);
      toast.success(t("chatNav.screenshot.copied"));
    } catch {
      toast.error(t("chatNav.screenshot.copyFailed"));
    }
  };
  return (
    <Dialog open={!!result && !closing} onOpenChange={(o) => !o && close()}>
      <DialogContent className="max-w-3xl">
        <DialogTitle>{t("chatNav.screenshot.previewTitle")}</DialogTitle>
        <DialogDescription>{t("chatNav.screenshot.previewDescription")}</DialogDescription>
        {result ? (
          <div className="max-h-[60dvh] overflow-auto rounded-lg border bg-muted/30">
            {/* eslint-disable-next-line @next/next/no-img-element */}
            <img src={result.url} alt={t("chatNav.screenshot.previewTitle")} className="mx-auto h-auto max-w-full" />
          </div>
        ) : null}
        <div className="flex flex-wrap justify-end gap-2">
          <Button variant="outline" onClick={copy}>
            <Copy /> {t("chatNav.screenshot.copy")}
          </Button>
          <Button
            onClick={() => {
              if (!result) return;
              downloadBlob(result.blob, screenshotFileName(title));
              toast.success(t("chatNav.screenshot.downloaded"));
            }}
          >
            <Download /> {t("chatNav.screenshot.download")}
          </Button>
          <Button variant="ghost" onClick={close}>
            <X /> {t("chatNav.screenshot.close")}
          </Button>
        </div>
      </DialogContent>
    </Dialog>
  );
}
