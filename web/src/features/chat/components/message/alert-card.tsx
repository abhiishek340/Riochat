"use client";

import { useState } from "react";
import { AlertTriangle, Check, ChevronDown, Copy, ShieldAlert } from "lucide-react";
import { codeToKey } from "@/lib/i18n/errors";
import { useI18n, useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { useCopy } from "../../hooks/use-copy";
import { upstreamSummary } from "../../lib/error-text";
import type { MessageAlert } from "../../types";
import { DebugViewer } from "./debug-viewer";

function knownCodeText(t: ReturnType<typeof useT>, code: string | undefined): string | null {
  if (code === "stream_interrupted" || code === "conversation_run.stream_interrupted") return t("chatMsg.alert.streamInterrupted");
  return null;
}

export function AlertCard({ alert, message }: { alert: MessageAlert; message?: string }) {
  const t = useT();
  const { lookup } = useI18n();
  const { copied, copy } = useCopy();
  const [debugOpen, setDebugOpen] = useState(false);

  if (alert.kind === "blocked") {
    return (
      <div role="alert" className="my-2 grid gap-1.5 rounded-xl border border-warning/40 bg-warning/10 p-3 text-sm">
        <div className="flex items-center gap-2 font-medium">
          <ShieldAlert className="size-4 text-warning-foreground dark:text-warning" />
          {t("chatMsg.alert.blockedTitle")}
        </div>
        {alert.lines.map((line) => (
          <p key={line.key} className="text-muted-foreground">
            {line.key === "description" ? t("chatMsg.alert.blockedDescription") : null}
            {line.key === "eventId" ? t("chatMsg.alert.eventId", { id: line.value ?? "" }) : null}
            {line.key === "categories" ? t("chatMsg.alert.categories", { categories: line.value ?? "" }) : null}
            {line.key === "billed" ? t("chatMsg.alert.billedAfterBlock") : null}
          </p>
        ))}
      </div>
    );
  }

  // The app's text for the error code wins; the raw server message is the fallback.
  const known = knownCodeText(t, alert.errorCode) ?? (alert.errorCode ? (lookup(codeToKey(alert.errorCode)) ?? null) : null);
  const reason = known ?? (alert.debug ? upstreamSummary({ message: alert.message ?? message ?? "", debug: alert.debug, fallback: alert.message ?? "" }) : (alert.message ?? message ?? ""));
  return (
    <div role="alert" className="my-2 grid gap-2 rounded-xl border border-destructive/30 bg-destructive/5 p-3 text-sm">
      <div className="flex items-center gap-2 font-medium text-destructive">
        <AlertTriangle className="size-4" />
        {t("chatMsg.alert.interruptedTitle")}
      </div>
      {reason ? <p className="whitespace-pre-wrap text-muted-foreground">{reason}</p> : null}
      <div className="flex flex-wrap items-center gap-2">
        {alert.errorId ? (
          <button
            type="button"
            onClick={() => alert.errorId && void copy(alert.errorId)}
            title={t("chatMsg.alert.errorIdHint")}
            className="inline-flex items-center gap-1.5 rounded-md border bg-background px-2 py-0.5 font-mono text-xs text-muted-foreground hover:text-foreground"
          >
            {copied ? <Check className="size-3" /> : <Copy className="size-3" />}
            <span>{t("chatMsg.alert.errorId")}</span> {alert.errorId}
          </button>
        ) : null}
        {alert.debug ? (
          <button type="button" onClick={() => setDebugOpen((v) => !v)} aria-expanded={debugOpen} className="inline-flex items-center gap-1 text-xs text-muted-foreground hover:text-foreground">
            <ChevronDown className={cn("size-3 transition-transform", debugOpen && "rotate-180")} /> {t("chatMsg.debug.title")}
          </button>
        ) : null}
      </div>
      {debugOpen && alert.debug ? <DebugViewer debug={alert.debug} /> : null}
    </div>
  );
}
