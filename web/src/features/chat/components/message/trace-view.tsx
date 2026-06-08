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

function ToolRow({ call, runId, allowFull, autoOpen }: { call: ToolCallView; runId: string; allowFull: boolean; autoOpen: boolean }) {
  const t = useT();
  const Icon = toolIcon(call.category);
  const active = call.status === "calling";
  const [manual, setManual] = useState<boolean | null>(null);
  const open = manual ?? (active && autoOpen);
  const [full, setFull] = useState(false);
  return (
    <div className="rounded-lg border bg-background/60">
      <button type="button" onClick={() => setManual(!open)} aria-expanded={open} className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs">
        <ChevronRight className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")} />
        <Icon className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="min-w-0 flex-1 truncate font-medium">{call.name || t(`chatRich.trace.category.${call.category}`)}</span>
        {call.latencyMs > 0 ? <span className="text-muted-foreground">{formatDuration(call.latencyMs)}</span> : null}
        <ToolStatusBadge status={call.status} />
      </button>
      {open ? (
        <div className="grid gap-2 border-t px-2.5 py-2 text-xs">
          {call.category === "web_search" && call.sources.length > 0 ? (
            <div className="grid gap-1.5">
              <div className="text-[11px] font-medium uppercase tracking-wide text-muted-foreground">{t("chatRich.trace.tool.sources", { count: call.sources.length })}</div>
              <ul className="grid gap-1.5">
                {call.sources.map((s, i) => (
                  <li key={s.url} className="min-w-0">
                    {isHttpUrl(s.url) ? (
                      <a href={s.url} target="_blank" rel="noreferrer noopener" className="inline-flex items-center gap-1 font-medium text-primary hover:underline">
                        <Globe className="size-3 shrink-0" />
                        <span className="truncate">{s.title || t("chatRich.trace.tool.sourceFallback", { index: i + 1 })}</span>
                      </a>
                    ) : null}
                    <div className="truncate text-muted-foreground">{new URL(s.url).host}</div>
                    {s.snippet ? <p className="line-clamp-2 text-muted-foreground">{s.snippet}</p> : null}
                  </li>
                ))}
              </ul>
            </div>
          ) : null}
          {call.category === "image" && call.images.length > 0 ? (
            <div className="flex flex-wrap gap-2">
              {call.images.map((src, i) => (
                // eslint-disable-next-line @next/next/no-img-element -- tool output images
                <img key={i} src={src.startsWith("http") || src.startsWith("data:") || src.startsWith("blob:") ? src : `data:image/png;base64,${src}`} alt={t("chatRich.trace.tool.imageAlt", { index: i + 1 })} className="max-h-40 rounded-md border" referrerPolicy="no-referrer" />
              ))}
            </div>
          ) : null}
          {call.category === "code" && call.exitCode !== null ? <p className="text-muted-foreground">{t("chatRich.trace.tool.exitCode", { code: call.exitCode })}</p> : null}
          <CodeLine title={t("chatRich.trace.tool.request")} text={call.input} />
          {call.category !== "web_search" || call.sources.length === 0 ? <CodeLine title={t("chatRich.trace.tool.response")} text={call.output} /> : null}
          <CodeLine title={t("chatRich.trace.tool.error")} text={call.error} />
          {allowFull && call.status !== "calling" && (call.detailRunId || runId) ? (
            <div>
              <Button variant="outline" size="sm" onClick={() => setFull(true)}>
                {t("chatRich.trace.tool.viewFull")}
              </Button>
              <FullResultDialog open={full} onOpenChange={setFull} runId={call.detailRunId || runId} call={call} />
            </div>
          ) : null}
        </div>
      ) : null}
    </div>
  );
}

function FullResultDialog({ open, onOpenChange, runId, call }: { open: boolean; onOpenChange: (o: boolean) => void; runId: string; call: ToolCallView }) {
  const t = useT();
  const [state, setState] = useState<{ status: "idle" | "loading" | "ready" | "error"; detail: ToolCallDetail | null }>({ status: "idle", detail: null });
  useEffect(() => {
    if (!open) {
      setState({ status: "idle", detail: null }); // closing resets, so a reopen refetches
      return;
    }
    let live = true;
    setState({ status: "loading", detail: null });
    fetchToolCall(runId, call.id).then(
      (detail) => live && setState({ status: "ready", detail }),
      () => live && setState({ status: "error", detail: null }),
    );
    return () => {
      live = false;
    };
  }, [open, runId, call.id]);
  const d = state.detail;
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="max-w-3xl">
        <DialogHeader>
          <DialogTitle>{t("chatRich.trace.tool.fullTitle")}</DialogTitle>
          <DialogDescription>{call.name}</DialogDescription>
        </DialogHeader>
        {state.status === "loading" ? <Skeleton className="h-40 w-full" /> : null}
        {state.status === "error" ? <p className="text-sm text-destructive">{t("chatRich.trace.tool.fullFailed")}</p> : null}
        {d ? (
          <div className="grid gap-3">
            {d.outputOmitted ? <p className="text-sm text-muted-foreground">{t("chatRich.trace.tool.omitted", { size: formatBytes(d.outputSizeBytes) })}</p> : d.outputJSON ? <CodeLine title={t("chatRich.trace.tool.response")} text={pretty(d.outputJSON)} /> : <p className="text-sm text-muted-foreground">{t("chatRich.trace.tool.empty")}</p>}
            {d.errorOmitted ? <p className="text-sm text-muted-foreground">{t("chatRich.trace.tool.omitted", { size: formatBytes(d.errorSizeBytes) })}</p> : d.errorJSON ? <CodeLine title={t("chatRich.trace.tool.error")} text={pretty(d.errorJSON)} /> : null}
          </div>
        ) : null}
      </DialogContent>
    </Dialog>
  );
}

function pretty(json: string): string {
  try {
    return JSON.stringify(JSON.parse(json), null, 2);
  } catch {
    return json;
  }
}

function ThinkingRow({ round, active, autoOpen }: { round: Round; active: boolean; autoOpen: boolean }) {
  const t = useT();
  const th = round.thinking;
  const [manual, setManual] = useState<boolean | null>(null);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!active) return;
    const id = setInterval(() => setNow(Date.now()), 500);
    return () => clearInterval(id);
  }, [active]);
  if (!th) return null;
  const open = manual ?? (active && autoOpen);
  const started = Date.parse(th.startedAt);
  const ended = th.endedAt ? Date.parse(th.endedAt) : NaN;
  const ms = Number.isFinite(started) ? (Number.isFinite(ended) ? ended - started : active ? now - started : 0) : 0;
  return (
    <div className="rounded-lg border bg-background/60">
      <button type="button" onClick={() => setManual(!open)} aria-expanded={open} className="flex w-full items-center gap-2 px-2.5 py-1.5 text-left text-xs">
        <ChevronRight className={cn("size-3.5 shrink-0 text-muted-foreground transition-transform", open && "rotate-90")} />
        <Brain className="size-3.5 shrink-0 text-muted-foreground" />
        <span className="font-medium">{active ? t("chatRich.trace.thinking.active") : t("chatRich.trace.thinking.done")}</span>
        {ms > 0 ? <span className="text-muted-foreground">{t("chatRich.trace.thinking.duration", { seconds: Math.max(1, Math.round(ms / 1000)) })}</span> : null}
        {!open && th.summary ? <span className="min-w-0 flex-1 truncate text-muted-foreground">{th.summary}</span> : null}
      </button>
      {open ? (
        <div className="border-t px-2.5 pb-2">
          <Markdown content={th.text || th.summary} variant="thinking" streaming={active} />
        </div>
      ) : null}
    </div>
  );
}

export const AgentTrace = memo(function AgentTrace({ message, live, active, hasAnswer }: { message: ChatMessage; live: readonly LiveThinkEntry[]; active: boolean; hasAnswer: boolean }) {
  const t = useT();
  const env = useMessageEnv();
  const cache = useStore(traceCache);
  const cached = cache[message.id];
  const summaryOnly = !!message.trace?.detailOmitted && !active && !env.temporary;
  const trace = cached?.status === "ready" && cached.trace ? cached.trace : message.trace;
  const rounds = useMemo(() => buildRounds(trace, live), [trace, live]);
  const totals = roundTotals(rounds);
  const [manual, setManual] = useState<boolean | null>(null);
  const open = manual ?? (active && !hasAnswer && env.prefs.autoExpandThinking);
  const [asked, setAsked] = useState(false);

  useEffect(() => {
    if (open && summaryOnly && !asked) {
      setAsked(true);
      void loadFullTrace(message.id);
    }
  }, [open, summaryOnly, asked, message.id]);

  if (rounds.length === 0 && !active) return null;
  if (rounds.length === 0) return null;
  const loading = summaryOnly && cached?.status !== "ready";
  const failed = cached?.status === "error";
  const first = rounds.find((r) => r.thinking?.startedAt)?.thinking?.startedAt;
  const lastEnd = [...rounds].reverse().find((r) => r.thinking?.endedAt)?.thinking?.endedAt;
  const dur = first && lastEnd ? Date.parse(lastEnd) - Date.parse(first) : 0;

  return (
    <div className="my-1.5 rounded-xl border bg-muted/30 text-sm" data-capture-hide>
      <button type="button" onClick={() => setManual(!open)} aria-expanded={open} className="flex w-full flex-wrap items-center gap-x-2 gap-y-0.5 px-3 py-1.5 text-left text-xs font-medium text-muted-foreground">
        <ChevronRight className={cn("size-3.5 transition-transform", open && "rotate-90")} />
        {active ? <Loader2 className="size-3.5 animate-spin" /> : <CheckCircle2 className="size-3.5" />}
        <span>{active ? t("chatRich.trace.runActive") : t("chatRich.trace.runDone")}</span>
        {totals.toolCalls > 0 ? <span className="font-normal">{t("chatRich.trace.toolCalls", { count: totals.toolCalls })}</span> : null}
        {totals.thinkRounds > 0 ? <span className="font-normal">{t("chatRich.trace.thinkRounds", { count: totals.thinkRounds })}</span> : null}
        {dur > 0 ? <span className="font-normal">{formatDuration(dur)}</span> : null}
      </button>
      {open ? (
        <div className="grid gap-2 border-t p-2.5">
          {loading ? <Skeleton className="h-10 w-full" /> : null}
          {failed ? (
            <div className="flex items-center gap-2 text-xs text-destructive">
              {t("chatRich.trace.loadFailed")}
              <Button size="sm" variant="outline" onClick={() => void loadFullTrace(message.id)}>
                {t("common.actions.retry")}
              </Button>
            </div>
          ) : null}
          {rounds.map((round) => (
            <div key={round.id} className="grid gap-1.5">
              <ThinkingRow round={round} active={active && !round.thinking?.endedAt} autoOpen={env.prefs.autoExpandThinking} />
              {round.tools.map((call) => (
                <ToolRow key={call.id} call={call} runId={message.runId} allowFull={!env.temporary && !active && message.persisted} autoOpen={env.prefs.autoExpandToolCalls} />
              ))}
            </div>
          ))}
        </div>
      ) : null}
    </div>
  );
});

