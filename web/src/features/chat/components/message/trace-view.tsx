"use client";

import { memo, useEffect, useMemo, useState } from "react";
import { Brain, CheckCircle2, ChevronRight, CircleAlert, Code2, FileText, Globe, Image as ImageIcon, Loader2, Search, Terminal, Wrench } from "lucide-react";
import { Badge, Button, Dialog, DialogContent, DialogDescription, DialogHeader, DialogTitle, Skeleton, Tooltip } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { fetchToolCall, type ToolCallDetail } from "../../api";
import { useCopy } from "../../hooks/use-copy";
import { formatBytes, formatDuration } from "../../lib/format";
import { buildRounds, isHttpUrl, isNoiseStage, parseStages, reasonForStateless, roundTotals, type LiveThinkEntry, type Round, type StageFile, type ToolCallView, type TraceStage } from "../../lib/trace";
import { traceCache, loadFullTrace } from "../../state/trace-cache";
import { useStore } from "../../state/store";
import type { ChatMessage, ProcessTraceDTO, PromptTraceDTO } from "../../types";
import { Markdown } from "../markdown/markdown";
import { useMessageEnv } from "./env";

// --- the process summary -----------------------------------------------------------------------------

const FILE_BADGE: Record<string, { key: "direct" | "budget" | "retrieval" | "full" | "native" | "unsupported" | "skipped" | "file"; tone: "primary" | "neutral" | "warning" | "outline" }> = {
  direct_images: { key: "direct", tone: "primary" },
  adaptive: { key: "budget", tone: "neutral" },
  retrieval: { key: "retrieval", tone: "neutral" },
  full_context: { key: "full", tone: "neutral" },
  native: { key: "native", tone: "primary" },
  unsupported: { key: "unsupported", tone: "warning" },
  skipped: { key: "skipped", tone: "warning" },
  other: { key: "file", tone: "outline" },
};

function FileBadges({ files }: { files: StageFile[] }) {
  const t = useT();
  return (
    <div className="mt-1 flex flex-wrap gap-1.5">
      {files.map((f, i) => {
        const b = FILE_BADGE[f.group] ?? { key: "file" as const, tone: "outline" as const };
        return (
          <Tooltip key={`${f.fileId}-${i}`} label={t(`chatRich.trace.fileBadge.${b.key}Hint`)}>
            <Badge tone={b.tone} className="max-w-48">
              <FileText className="size-3 shrink-0" />
              <span className="truncate">{f.name || t("chatRich.trace.fileBadge.file")}</span>
              <span className="text-[10px] opacity-70">{t(`chatRich.trace.fileBadge.${b.key}`)}</span>
            </Badge>
          </Tooltip>
        );
      })}
    </div>
  );
}

function stageTitle(t: ReturnType<typeof useT>, s: TraceStage): string {
  switch (s.kind) {
    case "context_planning":
      return t("chatRich.trace.stage.planning");
    case "content_retrieval":
      return t("chatRich.trace.stage.retrieval");
    case "file_context":
      return t("chatRich.trace.stage.files");
    case "context_compaction":
      return t("chatRich.trace.stage.compaction");
    case "skill_context":
      return t("chatRich.trace.stage.skills");
    default:
      return s.label || t("chatRich.trace.stage.other");
  }
}

function StageBody({ stage, prompt }: { stage: TraceStage; prompt: PromptTraceDTO | null | undefined }) {
  const t = useT();
  if (stage.kind === "file_context") {
    return (
      <div>
        <p>
          {t("chatRich.trace.filesIncluded", { count: stage.includedCount })}
          {stage.skippedCount > 0 ? `, ${t("chatRich.trace.filesSkipped", { count: stage.skippedCount })}` : ""}
        </p>
        {stage.files.length > 0 ? <FileBadges files={stage.files} /> : null}
      </div>
    );
  }
  if (stage.kind === "content_retrieval" && stage.retrieval) {
    const r = stage.retrieval;
    const status = r.status;
    let text: string;
    if (status === "completed") text = t("chatRich.trace.retrieval.completed", { chunks: r.chunkCount, files: r.fileCount });
    else if (status === "empty") text = r.fallback === "full_text" ? t("chatRich.trace.retrieval.emptyFallback") : t("chatRich.trace.retrieval.empty");
    else if (status === "low_score") text = r.fallback === "full_text" ? t("chatRich.trace.retrieval.lowScoreFallback") : t("chatRich.trace.retrieval.lowScore");
    else if (status === "skipped") text = t("chatRich.trace.retrieval.skipped");
    else text = r.fallback === "full_text" ? t("chatRich.trace.retrieval.incompleteFallback") : t("chatRich.trace.retrieval.incomplete");
    return <p>{text}</p>;
  }
  if (stage.kind === "context_compaction" && stage.compaction) {
    const c = stage.compaction;
    if (c.status === "pending") return <p>{t("chatRich.trace.compaction.pending")}</p>;
    if (c.status === "failed") return <p>{t("chatRich.trace.compaction.failed")}</p>;
    return (
      <p>
        {t("chatRich.trace.compaction.range", { from: c.fromTurn, to: c.toTurn })} · {t("chatRich.trace.compaction.tokens", { source: c.sourceTokens, summary: c.summaryTokens })}
      </p>
    );
  }
  if (stage.kind === "context_planning" && prompt) return <PromptTraceView prompt={prompt} />;
  return stage.detail ? <p>{stage.detail}</p> : null;
}

function PromptTraceView({ prompt }: { prompt: PromptTraceDTO }) {
  const t = useT();
  const mode = prompt.mode === "stateful" ? "stateful" : prompt.mode === "full_retry" ? "fullRetry" : "full";
  const reason = reasonForStateless(prompt.statefulDisabledReason);
  const blocks = prompt.blocks ?? [];
  const count = (kind: string) => blocks.filter((b) => b.kind === kind).length;
  const cacheable = blocks.filter((b) => b.cacheable).length;
  return (
    <div className="grid gap-0.5">
      <p>
        {t(`chatRich.trace.prompt.mode.${mode}`)} · {t("chatRich.trace.prompt.sent", { sent: prompt.sentMessageCount ?? 0, full: prompt.fullMessageCount ?? 0, tokens: prompt.sentTokenEstimate ?? 0 })}
      </p>
      {prompt.statefulSavedMessages ? <p>{t("chatRich.trace.prompt.saved", { messages: prompt.statefulSavedMessages, tokens: prompt.statefulSavedTokens ?? 0 })}</p> : null}
      {blocks.length > 0 ? (
        <p>
          {t("chatRich.trace.prompt.cacheable", { count: cacheable })} · {t("chatRich.trace.prompt.evidence", { count: count("historical_evidence") })} · {t("chatRich.trace.prompt.dynamic", { count: count("dynamic") })}
        </p>
      ) : null}
      {reason && prompt.mode !== "stateful" ? <p>{t(`chatRich.trace.prompt.reason.${reason}`)}</p> : null}
    </div>
  );
}

export const ProcessSummary = memo(function ProcessSummary({ trace, active, hasAnswer }: { trace: ProcessTraceDTO; active: boolean; hasAnswer: boolean }) {
  const t = useT();
  const stages = useMemo(() => parseStages(trace.process).filter((s) => !isNoiseStage(s)), [trace.process]);
  const [manual, setManual] = useState<boolean | null>(null);
  const open = manual ?? (active && !hasAnswer);
  if (stages.length === 0 && !trace.promptTrace) return null;
  const list = stages.length > 0 ? stages : [];
  return (
    <div className="my-1.5 rounded-xl border bg-muted/30 text-sm" data-capture-hide>
      <button type="button" onClick={() => setManual(!open)} aria-expanded={open} className="flex w-full items-center gap-1.5 px-3 py-1.5 text-left text-xs font-medium text-muted-foreground">
        <ChevronRight className={cn("size-3.5 transition-transform", open && "rotate-90")} />
        {active ? <Loader2 className="size-3.5 animate-spin" /> : <CheckCircle2 className="size-3.5" />}
        {active ? t("chatRich.trace.processActive") : t("chatRich.trace.processDone")}
      </button>
      {open ? (
        <ul className="grid gap-2 border-t px-3 py-2 text-xs text-muted-foreground">
          {list.map((s, i) => (
            <li key={i} className="grid gap-0.5">
              <span className="font-medium text-foreground">{stageTitle(t, s)}</span>
              <StageBody stage={s} prompt={s.kind === "context_planning" ? trace.promptTrace : null} />
            </li>
          ))}
          {list.length === 0 && trace.promptTrace ? <PromptTraceView prompt={trace.promptTrace} /> : null}
        </ul>
      ) : null}
    </div>
  );
});

// --- agent trace (thinking and tools) -------------------------------------------------------------------

function toolIcon(c: ToolCallView["category"]) {
  switch (c) {
    case "web_search":
      return Search;
    case "code":
      return Code2;
    case "image":
      return ImageIcon;
    case "shell":
      return Terminal;
    default:
      return Wrench;
  }
}

function ToolStatusBadge({ status }: { status: ToolCallView["status"] }) {
  const t = useT();
  if (status === "calling") return <Badge tone="primary"><Loader2 className="size-3 animate-spin" />{t("chatRich.trace.tool.calling")}</Badge>;
  if (status === "failed") return <Badge tone="danger"><CircleAlert className="size-3" />{t("chatRich.trace.tool.failed")}</Badge>;
  if (status === "reused") return <Badge tone="neutral">{t("chatRich.trace.tool.reused")}</Badge>;
  return <Badge tone="success"><CheckCircle2 className="size-3" />{t("chatRich.trace.tool.completed")}</Badge>;
}

function CodeLine({ title, text }: { title: string; text: string }) {
  const t = useT();
  const { copied, copy } = useCopy();
  if (!text) return null;
  return (
    <div className="grid gap-1">
      <div className="flex items-center justify-between text-[11px] font-medium uppercase tracking-wide text-muted-foreground">
        {title}
        <button type="button" onClick={() => void copy(text)} className="normal-case hover:text-foreground">
          {copied ? t("common.actions.copied") : t("common.actions.copy")}
        </button>
      </div>
      <pre className="max-h-56 overflow-auto rounded-lg bg-muted/60 p-2 font-mono text-xs whitespace-pre-wrap break-all">{text}</pre>
    </div>
  );
}
