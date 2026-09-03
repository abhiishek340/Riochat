"use client";

import { useEffect, useMemo, useRef, useState } from "react";
import { Code2, Loader2 } from "lucide-react";
import { Button } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { WIDGET_MAX_BYTES, widgetTitle } from "../../lib/blocks/block";
import { clampHeight, WIDGET_HEIGHT_MESSAGE, widgetDocument } from "../../lib/blocks/widget-doc";
import { captureTheme, PERMISSIONS_POLICY, SANDBOX_ATTR } from "../../lib/sandbox-doc";
import { CodeBlock } from "../markdown/code-block";

/** A model-written HTML widget, run inside a script-only sandbox. */
export function WidgetHost({ code, closed }: { code: string; closed: boolean }) {
  const t = useT();
  const [showSource, setShowSource] = useState(false);
  const [height, setHeight] = useState(180);
  const frame = useRef<HTMLIFrameElement>(null);
  const title = useMemo(() => widgetTitle(code), [code]);
  const tooLarge = useMemo(() => new TextEncoder().encode(code).length > WIDGET_MAX_BYTES, [code]);
  const doc = useMemo(() => (closed && !tooLarge && code.trim() ? widgetDocument(code, captureTheme()) : ""), [code, closed, tooLarge]);

  useEffect(() => {
    const onMessage = (e: MessageEvent) => {
      if (!frame.current || e.source !== frame.current.contentWindow) return;
      const data = e.data as { type?: unknown; height?: unknown } | null;
      if (!data || data.type !== WIDGET_HEIGHT_MESSAGE) return;
      const h = clampHeight(data.height);
      if (h !== null) setHeight(h);
    };
    window.addEventListener("message", onMessage);
    return () => window.removeEventListener("message", onMessage);
  }, []);

  if (!closed) {
    return (
      <div className="my-3 flex items-center gap-2 rounded-xl border bg-muted/30 px-3 py-4 text-sm text-muted-foreground" role="status">
        <Loader2 className="size-4 animate-spin" />
        {title ? t("chatBlocks.widget.generatingTitled", { title }) : t("chatBlocks.widget.generating")}
      </div>
    );
  }
  if (tooLarge || !doc) {
    return (
      <div className="my-3 rounded-xl border border-dashed px-3 py-3 text-sm text-muted-foreground">
        {tooLarge ? t("chatBlocks.widget.tooLarge") : t("chatBlocks.widget.unavailable")}
      </div>
    );
  }
  return (
    <div className="my-3 overflow-hidden rounded-xl border" data-widget>
      <div className="flex items-center justify-between gap-2 border-b bg-muted/40 px-3 py-1">
        <span className="truncate text-xs text-muted-foreground">{title || t("chatBlocks.widget.title")}</span>
        <Button variant="ghost" size="sm" className="h-6 gap-1 px-2 text-xs" onClick={() => setShowSource((v) => !v)} aria-pressed={showSource} data-capture-hide>
          <Code2 className="size-3.5" />
          {showSource ? t("chatBlocks.widget.hideSource") : t("chatBlocks.widget.viewSource")}
        </Button>
      </div>
      {showSource ? (
        <CodeBlock code={code} lang="html" closed />
      ) : (
        <iframe ref={frame} title={title || t("chatBlocks.widget.title")} sandbox={SANDBOX_ATTR} referrerPolicy="no-referrer" allow={PERMISSIONS_POLICY} srcDoc={doc} style={{ height }} className="block w-full border-0 bg-background" />
      )}
    </div>
  );
}
