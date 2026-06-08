"use client";

import { useMemo, useState } from "react";
import { Check, Copy } from "lucide-react";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { useCopy } from "../../hooks/use-copy";
import { formatBytes } from "../../lib/format";
import type { DebugSnapshot } from "../../types";

type Side = "request" | "response";
type Mode = "formatted" | "raw";

function tryParse(text: string): unknown {
  try {
    return JSON.parse(text);
  } catch {
    return undefined;
  }
}

/** Events of a streamed body: each `data:` payload on its own. */
export function streamEvents(body: string): { index: number; text: string; json: unknown }[] {
  return body
    .split(/\r?\n/)
    .filter((l) => l.startsWith("data:"))
    .map((l) => l.slice(5).trim())
    .filter((l) => l && l !== "[DONE]")
    .map((text, index) => ({ index, text, json: tryParse(text) }));
}

function Tree({ value, depth = 0, label }: { value: unknown; depth?: number; label?: string }) {
  if (value !== null && typeof value === "object") {
    const entries = Array.isArray(value) ? value.map((v, i) => [String(i), v] as const) : Object.entries(value as Record<string, unknown>);
    const tag = Array.isArray(value) ? `[${entries.length}]` : "";
    const role = !Array.isArray(value) && typeof (value as Record<string, unknown>).role === "string" ? String((value as Record<string, unknown>).role) : "";
    return (
      <div className={cn(depth > 0 && "ml-3 border-l pl-2")}>
        {label !== undefined ? (
          <div className="text-xs">
            <span className="font-medium">{label}</span> <span className="text-muted-foreground">{tag}</span>
            {role ? <span className="ml-1 rounded bg-muted px-1 text-[10px]">{role}</span> : null}
          </div>
        ) : null}
        {entries.map(([k, v]) => (
          <Tree key={k} value={v} label={k} depth={depth + 1} />
        ))}
      </div>
    );
  }
  return (
    <div className={cn("flex gap-2 text-xs", depth > 0 && "ml-3 border-l pl-2")}>
      {label !== undefined ? <span className="shrink-0 font-medium">{label}</span> : null}
      <span className="min-w-0 whitespace-pre-wrap break-all text-muted-foreground">{typeof value === "string" ? value : JSON.stringify(value)}</span>
    </div>
  );
}

export function DebugViewer({ debug }: { debug: DebugSnapshot }) {
  const t = useT();
  const [side, setSide] = useState<Side>("response");
  const [mode, setMode] = useState<Mode>("formatted");
  const { copied, copy } = useCopy();
  const body = side === "request" ? debug.requestBody : debug.responseBody;
  const headers = side === "request" ? debug.requestHeaders : debug.responseHeaders;
  const streamed = /^data:/m.test(body.trim());
  const events = useMemo(() => (streamed ? streamEvents(body) : []), [body, streamed]);
  const json = useMemo(() => (streamed ? undefined : tryParse(body)), [body, streamed]);
  const headerText = Object.entries(headers).map(([k, v]) => `${k}: ${v}`).join("\n");
  const raw = [headerText, body].filter(Boolean).join("\n\n");

  return (
    <div className="grid gap-2 rounded-lg border bg-background p-2.5">
      <div className="flex flex-wrap items-center gap-1.5 text-xs">
        {(["request", "response"] as const).map((s) => (
          <button key={s} type="button" onClick={() => setSide(s)} aria-pressed={side === s} className={cn("rounded-md px-2 py-0.5", side === s ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:text-foreground")}>
            {t(s === "request" ? "chatMsg.debug.request" : "chatMsg.debug.response")}
          </button>
        ))}
        <span className="mx-1 h-3 w-px bg-border" />
        {(["formatted", "raw"] as const).map((m) => (
          <button key={m} type="button" onClick={() => setMode(m)} aria-pressed={mode === m} className={cn("rounded-md px-2 py-0.5", mode === m ? "bg-accent text-accent-foreground" : "text-muted-foreground hover:text-foreground")}>
            {t(m === "formatted" ? "chatMsg.debug.formatted" : "chatMsg.debug.raw")}
          </button>
        ))}
        <span className="ml-auto text-muted-foreground">
          {debug.method} {debug.path} {debug.status ? `· ${debug.status}` : ""} · {formatBytes(body.length)}
        </span>
        <button type="button" onClick={() => void copy(raw)} aria-label={t("common.actions.copy")} className="rounded p-1 text-muted-foreground hover:text-foreground">
          {copied ? <Check className="size-3.5" /> : <Copy className="size-3.5" />}
        </button>
      </div>
      {side === "response" && debug.status >= 200 && debug.status < 300 && streamed ? <p className="rounded-md bg-warning/10 px-2 py-1 text-xs text-muted-foreground">{t("chatMsg.debug.streamIncompatible")}</p> : null}
      <div className="max-h-72 overflow-auto rounded-md bg-muted/50 p-2">
        {!body && !headerText ? (
          <p className="text-xs text-muted-foreground">{t("chatMsg.debug.empty")}</p>
        ) : mode === "raw" ? (
          <pre className="whitespace-pre-wrap break-all font-mono text-xs">{raw}</pre>
        ) : streamed ? (
          events.map((e) => (
            <div key={e.index} className="mb-2">
              <div className="text-[10px] uppercase text-muted-foreground">#{e.index + 1}</div>
              {e.json !== undefined ? <Tree value={e.json} /> : <pre className="whitespace-pre-wrap break-all font-mono text-xs">{e.text}</pre>}
            </div>
          ))
        ) : json !== undefined ? (
          <Tree value={json} />
        ) : (
          <pre className="whitespace-pre-wrap break-all font-mono text-xs">{body}</pre>
        )}
      </div>
    </div>
  );
}
