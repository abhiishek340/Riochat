"use client";

import { useEffect, useState } from "react";
import { toast } from "@/components/ui";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { cleanContentType, isActiveContentType, isReadyLikeStatus, previewKindOf, settleKind, type PreviewKind } from "../file-kind";
import { hasSession } from "../session";
import { useLatest } from "../use-latest";

export interface PreviewSubject {
  id: string;
  fileName: string;
  mimeType: string;
  /** Library files carry a lifecycle status; knowledge-base files do not (they are always readable). */
  status?: string;
  updatedAt: string;
}

export type PreviewState =
  | { phase: "idle" }
  | { phase: "loading" }
  | { phase: "error"; reason: "failed" | "processing" | "signin" | "load"; message?: string }
  | {
      phase: "ready";
      kind: PreviewKind;
      /** What the viewers render from (an SVG must keep its image type for `<img>`). */
      url: string;
      /** What "open in a new tab" and "download" use: never a type the browser would run as a page. */
      openUrl: string;
      text: string;
      contentType: string;
      size: number;
    };

/** Text viewers never render more than this many characters. */
export const MAX_PREVIEW_CHARS = 1_000_000;
const SAMPLE_BYTES = 16_000;

/**
 * Loads the bytes behind a file and works out how to show them. The effect
 * re-runs when the file, its status or its update time changes; answers that
 * arrive after the file changed are dropped and their object URLs released.
 */
export function usePreview(
  subject: PreviewSubject | null,
  load: (signal: AbortSignal) => Promise<Response>,
): PreviewState {
  const t = useT();
  const errorMessage = useErrorMessage();
  const [state, setState] = useState<PreviewState>({ phase: "idle" });
  const loadRef = useLatest(load);
  const textRef = useLatest({ t, errorMessage });
  const nameRef = useLatest({ fileName: subject?.fileName ?? "", mimeType: subject?.mimeType ?? "" });

  const id = subject?.id;
  const status = subject?.status;
  const updatedAt = subject?.updatedAt;

  useEffect(() => {
    if (id === undefined) {
      setState({ phase: "idle" });
      return;
    }
    if (status !== undefined && !isReadyLikeStatus(status)) {
      setState({ phase: "error", reason: status.trim().toLowerCase() === "failed" ? "failed" : "processing" });
      return;
    }
    const controller = new AbortController();
    let objectUrl: string | null = null;
    let inertUrl: string | null = null;
    let cancelled = false;
    setState({ phase: "loading" });

    void (async () => {
      try {
        if (!(await hasSession())) {
          if (!cancelled) setState({ phase: "error", reason: "signin" });
          return;
        }
        const response = await loadRef.current(controller.signal);
        const blob = await response.blob();
        if (cancelled) return;
        const contentType = cleanContentType(response.headers.get("content-type")) || cleanContentType(blob.type) || "application/octet-stream";
        const size = Number(response.headers.get("content-length")) || blob.size;
        let kind = previewKindOf({ fileName: nameRef.current.fileName, contentType, fallbackMime: nameRef.current.mimeType });
        let text = "";
        const wantsText = kind === "markdown" || kind === "code" || kind === "text" || kind === "spreadsheet";
        if (wantsText) {
          text = await blob.slice(0, MAX_PREVIEW_CHARS * 3).text();
          if (text.length > MAX_PREVIEW_CHARS) text = text.slice(0, MAX_PREVIEW_CHARS);
          kind = settleKind(kind, text);
        } else if (kind === "unsupported") {
          const sample = await blob.slice(0, SAMPLE_BYTES).text();
          kind = settleKind(kind, sample);
          if (kind === "text") text = (await blob.slice(0, MAX_PREVIEW_CHARS * 3).text()).slice(0, MAX_PREVIEW_CHARS);
        }
        if (cancelled) return;
        objectUrl = URL.createObjectURL(blob);
        if (isActiveContentType(contentType)) inertUrl = URL.createObjectURL(new Blob([blob], { type: "text/plain" }));
        setState({ phase: "ready", kind, url: objectUrl, openUrl: inertUrl ?? objectUrl, text, contentType, size });
      } catch (error) {
        if (cancelled || isAbortError(error)) return;
        const message = textRef.current.errorMessage(error);
        setState({ phase: "error", reason: "load", message });
        toast.error(textRef.current.t("filesWs.toasts.previewFailed"), { id: "files-preview-failed", description: message });
      }
    })();

    return () => {
      cancelled = true;
      controller.abort();
      if (objectUrl) URL.revokeObjectURL(objectUrl);
      if (inertUrl) URL.revokeObjectURL(inertUrl);
    };
  }, [id, status, updatedAt, loadRef, nameRef, textRef]);

  return state;
}
