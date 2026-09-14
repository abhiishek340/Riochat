"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { renderToPng } from "../lib/dom-render";
import { TooLargeError } from "../lib/screenshot";

export interface ScreenshotResult {
  url: string;
  blob: Blob;
}

function pageBackground(): string {
  for (const el of [document.body, document.documentElement]) {
    const bg = getComputedStyle(el).backgroundColor;
    if (bg && bg !== "transparent" && bg !== "rgba(0, 0, 0, 0)") return bg;
  }
  return "#ffffff";
}

/** Captures the message column to a PNG; one capture at a time, released when replaced or on unmount. */
export function useScreenshot(brand: string) {
  const t = useT();
  const [result, setResult] = useState<ScreenshotResult | null>(null);
  const [busy, setBusy] = useState(false);
  const running = useRef<AbortController | null>(null);

  const release = useCallback(() => {
    setResult((cur) => {
      if (cur) URL.revokeObjectURL(cur.url);
      return null;
    });
  }, []);

  const abort = useCallback(() => running.current?.abort(), []);

  useEffect(
    () => () => {
      running.current?.abort();
    },
    [],
  );

  const capture = useCallback(
    async (rows: ReadonlySet<string>) => {
      if (running.current) return;
      if (rows.size === 0) {
        toast.warning(t("chatNav.screenshot.empty"));
        return;
      }
      const root = document.querySelector<HTMLElement>("[data-message-inner]");
      if (!root) return;
      const controller = new AbortController();
      running.current = controller;
      setBusy(true);
      const progress = toast.loading(t("chatNav.screenshot.generating"));
      let settled = false;
      root.classList.add("rio-capture");
      try {
        // Let the capture styles apply before the DOM is read.
        await new Promise<void>((r) => requestAnimationFrame(() => r()));
        const blob = await renderToPng(root, { keepRows: rows, background: pageBackground(), brand, signal: controller.signal });
        if (controller.signal.aborted) return;
        setResult((cur) => {
          if (cur) URL.revokeObjectURL(cur.url);
          return { blob, url: URL.createObjectURL(blob) };
        });
        toast.success(t("chatNav.screenshot.ready"), { id: progress });
        settled = true;
      } catch (e) {
        if (controller.signal.aborted || (e instanceof DOMException && e.name === "AbortError")) return;
        settled = true;
        if (e instanceof TooLargeError) toast.error(t("chatNav.screenshot.tooLarge"), { id: progress });
        else toast.error(t("chatNav.screenshot.failed", { reason: e instanceof Error ? e.message : String(e) }), { id: progress });
      } finally {
        root.classList.remove("rio-capture");
        if (!settled) toast.dismiss(progress);
        running.current = null;
        setBusy(false);
      }
    },
    [brand, t],
  );

  return { result, busy, capture, release, abort };
}
