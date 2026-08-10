"use client";

import { useMemo, useState } from "react";
import { FileQuestion, Minus, Plus, RotateCcw, TriangleAlert } from "lucide-react";
import { Button, EmptyState, Spinner, Tooltip } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { MarkdownLite } from "./markdown-lite";
import { parseCsv } from "./csv";
import type { PreviewState } from "./use-preview";

const MAX_SHEET_ROWS = 500;

function ImageViewer({ url, name }: { url: string; name: string }) {
  const t = useT();
  const [zoom, setZoom] = useState(1);
  const clamp = (v: number) => Math.min(4, Math.max(0.25, Math.round(v * 100) / 100));
  return (
    <div className="relative flex h-full min-h-64 flex-col">
      <div className="absolute right-3 top-3 z-10 flex items-center gap-1 rounded-lg border bg-popover/90 p-1 shadow-sm backdrop-blur">
        <Tooltip label={t("filesWs.preview.zoomOut")}>
          <Button variant="ghost" size="icon-xs" aria-label={t("filesWs.preview.zoomOut")} onClick={() => setZoom((z) => clamp(z - 0.25))}>
            <Minus />
          </Button>
        </Tooltip>
        <span className="min-w-10 text-center text-xs tabular-nums text-muted-foreground">{Math.round(zoom * 100)}%</span>
        <Tooltip label={t("filesWs.preview.zoomIn")}>
          <Button variant="ghost" size="icon-xs" aria-label={t("filesWs.preview.zoomIn")} onClick={() => setZoom((z) => clamp(z + 0.25))}>
            <Plus />
          </Button>
        </Tooltip>
        <Tooltip label={t("filesWs.preview.zoomReset")}>
          <Button variant="ghost" size="icon-xs" aria-label={t("filesWs.preview.zoomReset")} onClick={() => setZoom(1)}>
            <RotateCcw />
          </Button>
        </Tooltip>
      </div>
      <div className="flex min-h-0 flex-1 items-center justify-center overflow-auto p-4">
        {/* eslint-disable-next-line @next/next/no-img-element -- a local blob URL, nothing to optimise */}
        <img
          src={url}
          alt={t("filesWs.preview.imageAlt", { name })}
          style={{ transform: `scale(${zoom})`, transformOrigin: "center" }}
          className="max-h-full max-w-full rounded-md object-contain transition-transform duration-150"
        />
      </div>
    </div>
  );
}

function SheetViewer({ text }: { text: string }) {
  const t = useT();
  const { rows, truncated } = useMemo(() => parseCsv(text, MAX_SHEET_ROWS), [text]);
  const width = rows.reduce((m, r) => Math.max(m, r.length), 0);
  return (
    <div className="grid gap-2 p-4">
      <div className="overflow-auto rounded-lg border">
        <table className="w-full border-collapse text-sm">
          {rows.length > 0 ? (
            <thead className="bg-muted/60 text-left font-medium">
              <tr>
                {Array.from({ length: width }, (_, c) => (
                  <th key={c} scope="col" className="max-w-72 truncate border-l px-3 py-1.5 font-medium first:border-l-0" title={rows[0]?.[c]}>
                    {rows[0]?.[c] ?? ""}
                  </th>
                ))}
              </tr>
            </thead>
          ) : null}
          <tbody>
            {rows.slice(1).map((row, r) => (
              <tr key={r} className="border-t">
                {Array.from({ length: width }, (_, c) => (
                  <td key={c} className="max-w-72 truncate border-l px-3 py-1.5 first:border-l-0" title={row[c]}>
                    {row[c] ?? ""}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <p className="text-xs text-muted-foreground">
        {t("filesWs.preview.rows", { count: rows.length })}
        {truncated ? ` · ${t("filesWs.preview.truncated", { count: MAX_SHEET_ROWS })}` : ""}
      </p>
    </div>
  );
}

export function PlainText({ text }: { text: string }) {
  return (
    <pre className="m-0 min-h-full whitespace-pre-wrap break-words p-4 font-mono text-[13px] leading-relaxed">{text}</pre>
  );
}

/** Renders whatever `usePreview` settled on. Fills its parent and scrolls on its own. */
export function PreviewPane({ state, fileName }: { state: PreviewState; fileName: string }) {
  const t = useT();
  if (state.phase === "idle") return null;
  if (state.phase === "loading") {
    return (
      <div className="flex h-full min-h-48 items-center justify-center gap-2 text-sm text-muted-foreground" role="status">
        <Spinner />
        {t("filesWs.preview.loading")}
      </div>
    );
  }
  if (state.phase === "error") {
    const message =
      state.reason === "failed"
        ? t("filesWs.preview.failedProcessing")
        : state.reason === "processing"
          ? t("filesWs.preview.stillProcessing")
          : state.reason === "signin"
            ? t("filesWs.preview.signIn")
            : state.message || t("filesWs.preview.loadFailed");
    return <EmptyState icon={<TriangleAlert />} title={t("filesWs.preview.errorTitle")} description={message} className="h-full" />;
  }
  switch (state.kind) {
    case "image":
      return <ImageViewer url={state.url} name={fileName} />;
    case "pdf":
      return <iframe src={state.url} title={fileName} className="h-full min-h-96 w-full border-0 bg-background" />;
    case "audio":
      return (
        <div className="flex h-full min-h-48 items-center justify-center p-6">
          <audio controls src={state.url} aria-label={t("filesWs.preview.mediaLabel", { name: fileName })} className="w-full max-w-lg" />
        </div>
      );
    case "video":
      return (
        <div className="flex h-full items-center justify-center bg-black/90 p-2">
          <video controls src={state.url} aria-label={t("filesWs.preview.mediaLabel", { name: fileName })} className="max-h-full max-w-full rounded-md" />
        </div>
      );
    case "markdown":
      return (
        <div className="p-5">
          <MarkdownLite source={state.text} />
        </div>
      );
    case "code":
    case "text":
      return <PlainText text={state.text} />;
    case "spreadsheet":
      return <SheetViewer text={state.text} />;
    case "docx":
    case "native":
      return <EmptyState icon={<FileQuestion />} title={t("filesWs.preview.unsupportedTitle")} description={t("filesWs.preview.officeBody")} className="h-full" />;
    default:
      return <EmptyState icon={<FileQuestion />} title={t("filesWs.preview.unsupportedTitle")} description={t("filesWs.preview.unsupportedBody")} className="h-full" />;
  }
}
