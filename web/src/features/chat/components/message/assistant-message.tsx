"use client";

import { memo, useEffect, useMemo, useState } from "react";
import { Bot, Check, Copy, CornerDownRight, Film, GitFork, Pencil, Play, RotateCcw, ThumbsDown, ThumbsUp, Trash2 } from "lucide-react";
import { Badge, Popover, PopoverContent, PopoverTrigger, Tooltip, Button, Input, toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { useBlockHostApi } from "../../hooks/use-block-host";
import { useCopy } from "../../hooks/use-copy";
import { shouldShowCost } from "../../lib/billing";
import { readableMessageText } from "../../lib/copy-text";
import { formatLatency, replyLatencyMs } from "../../lib/format";
import { detectInlineImage } from "../../lib/inline-image";
import { modelLabel } from "../../lib/model-caps";
import type { Navigator } from "../../lib/tree";
import type { LiveThinkEntry } from "../../lib/trace";
import { chatEngine } from "../../state/engine-instance";
import { useStore } from "../../state/store";
import { stageCitations } from "../../lib/trace";
import type { ChatAttachment, ChatMessage, KnowledgeSource } from "../../types";
import { Markdown } from "../markdown/markdown";
import { AlertCard } from "./alert-card";
import { AttachmentCards, GeneratedImages, VideoPlayer, isVideoAttachment } from "./attachment-cards";
import { BillingDetails } from "./billing-tooltip";
import { useMessageEnv } from "./env";
import { BranchNav, IconAction, MessageTime } from "./parts";
import { SourcesButton } from "./sources-dialog";
import { AgentTrace, ProcessSummary } from "./trace-view";
import { EditBox } from "./user-message";

const NO_LIVE: readonly LiveThinkEntry[] = [];

function useLiveThinking(runId: string): readonly LiveThinkEntry[] {
  const all = useStore(chatEngine.liveThinking);
  return all[runId] ?? NO_LIVE;
}

function stripVideoPlaceholders(content: string): string {
  return content.replace(/^[ \t]*!?\[Generated video[^\]]*\]\([^)]*\)[ \t]*\r?\n?/gim, "");
}

/** The card itself: shimmer (or the latest partial image) with a status line. */
export function GenerationCard({ shape, video, text, preview }: { shape: "wide" | "portrait" | "square"; video: boolean; text: string; preview?: string }) {
  const t = useT();
  const ratio = video ? "16 / 9" : shape === "portrait" ? "3 / 4" : shape === "square" ? "1 / 1" : "4 / 3";
  return (
    <div className="relative my-2 w-full max-w-md overflow-hidden rounded-2xl border" style={{ aspectRatio: ratio }} role="status" aria-label={text}>
      {preview ? (
        // eslint-disable-next-line @next/next/no-img-element -- partial image from the stream
        <img src={preview} alt={t("chatMsg.imagePreviewAlt")} className="absolute inset-0 size-full object-cover" />
      ) : (
        <div className="rio-shimmer absolute inset-0" />
      )}
      <div className="absolute inset-x-0 bottom-0 flex items-center gap-2 bg-gradient-to-t from-background/90 to-transparent px-3 py-2 text-xs text-muted-foreground">
        <span className="rio-typing"><span /><span /><span /></span>
        {text}
      </div>
    </div>
  );
}

/** Shown while an image or video is being generated. */
function GenerationPlaceholder({ message }: { message: ChatMessage }) {
  const t = useT();
  const live = message.live;
  const video = message.contentType === "video";
  const label = live?.label;
  let text = video ? t("chatMsg.generatingVideo") : t("chatMsg.generatingImage");
  if (label?.kind === "media") text = label.message || (label.status ? t(`chatMsg.media.${label.status === "queued" ? "queued" : label.status === "saving_artifact" ? "saving" : "running"}${video ? "Video" : "Image"}` as "chatMsg.media.queuedImage") : text);
  return <GenerationCard shape={live?.shape ?? "wide"} video={video} text={text} preview={live?.preview} />;
}

function ActivityLine({ message }: { message: ChatMessage }) {
  const t = useT();
  const label = message.live?.label;
  if (!label) {
    return (
      <div className="flex items-center gap-2 py-2 text-sm text-muted-foreground" role="status" aria-label={t("chatMsg.thinkingStatus")}>
        <span className="rio-typing"><span /><span /><span /></span>
      </div>
    );
  }
  const text =
    label.kind === "file_proc"
      ? label.message || t("chatMsg.processingFiles")
      : label.kind === "rag_search"
        ? label.message || t("chatMsg.retrieving")
        : label.kind === "moderation"
          ? t("chatMsg.safetyCheck")
          : label.message || t("chatMsg.thinkingStatus");
  return (
    <div className="flex items-center gap-2 py-2 text-sm text-muted-foreground" role="status">
      <span className="rio-typing"><span /><span /><span /></span>
      {text}
    </div>
  );
}

function RememberPopover({ onSave }: { onSave: (name: string, value: string) => Promise<boolean> }) {
  const t = useT();
  const [open, setOpen] = useState(false);
  const [name, setName] = useState("");
  const [value, setValue] = useState("");
  const [busy, setBusy] = useState(false);
  return (
    <Popover open={open} onOpenChange={setOpen}>
      <Tooltip label={t("chatMsg.remember.trigger")}>
        <PopoverTrigger asChild>
          <button type="button" aria-label={t("chatMsg.remember.trigger")} className="inline-flex size-7 items-center justify-center rounded-md text-muted-foreground hover:bg-accent hover:text-accent-foreground [&_svg]:size-3.5">
            <CornerDownRight />
          </button>
        </PopoverTrigger>
      </Tooltip>
      <PopoverContent className="w-72 p-3" align="end">
        <form
          className="grid gap-2"
          onSubmit={async (e) => {
            e.preventDefault();
            if (!name.trim() || !value.trim()) return;
            setBusy(true);
            const ok = await onSave(name.trim(), value.trim());
            setBusy(false);
            if (ok) {
              setOpen(false);
              setName("");
              setValue("");
            }
          }}
        >
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder={t("chatMsg.remember.name")} aria-label={t("chatMsg.remember.name")} maxLength={128} />
          <Input value={value} onChange={(e) => setValue(e.target.value)} placeholder={t("chatMsg.remember.value")} aria-label={t("chatMsg.remember.value")} />
          <Button type="submit" size="sm" loading={busy} disabled={!name.trim() || !value.trim()}>
            {t("chatMsg.remember.save")}
          </Button>
        </form>
      </PopoverContent>
    </Popover>
  );
}

function MetaRow({ message, nav, sources, onEdit, getCopyText }: { message: ChatMessage; nav?: Navigator; sources: readonly KnowledgeSource[]; onEdit: () => void; getCopyText: () => string }) {
  const t = useT();
  const env = useMessageEnv();
  const { copied, copy } = useCopy();
  const live = message.live?.streaming === true;
  const pending = message.status === "pending";
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (!live) return;
    const id = setInterval(() => setNow(Date.now()), 100);
    return () => clearInterval(id);
  }, [live]);
  const prefs = env.prefs;
  const usage = message.usage;
  const tokenTotal = usage.input + usage.output + usage.cacheRead + usage.cacheWrite + usage.reasoning;
  const latency = replyLatencyMs({ live, startedAt: message.live?.startedAt ?? 0, now, latencyMs: message.latencyMs, createdAt: message.createdAt, updatedAt: message.updatedAt });
  const nf = new Intl.NumberFormat(env.locale);
  const showCost = prefs.showBillingCost && shouldShowCost(message.billing, env.billingMode);
  const actionsOff = live || pending;

  return (
    <div className="mt-1 flex flex-wrap items-center gap-x-2 gap-y-1" data-meta>
      <div className="flex flex-wrap items-center gap-1.5">
        {prefs.showModelInfo && message.model ? (
          <Badge tone="outline" className="max-w-48" data-capture-only="" title={message.model}>
            <Bot className="size-3 shrink-0" />
            <span className="truncate">{modelLabel(message.model)}</span>
          </Badge>
        ) : null}
        {prefs.showTokenUsage && tokenTotal > 0 ? (
          <Tooltip
            label={
              <span className="grid gap-0.5 text-left">
                <span>{t("chatMsg.tokens.input")}: {nf.format(usage.input)}</span>
                <span>{t("chatMsg.tokens.output")}: {nf.format(usage.output)}</span>
                {usage.cacheRead > 0 ? <span>{t("chatMsg.tokens.cacheRead")}: {nf.format(usage.cacheRead)}</span> : null}
                {usage.cacheWrite > 0 ? <span>{t("chatMsg.tokens.cacheWrite")}: {nf.format(usage.cacheWrite)}</span> : null}
                {usage.reasoning > 0 ? <span>{t("chatMsg.tokens.reasoning")}: {nf.format(usage.reasoning)}</span> : null}
              </span>
            }
          >
            <Badge tone="outline" tabIndex={0}>
              {nf.format(usage.input)} ↑ {nf.format(usage.output + usage.reasoning)} ↓
            </Badge>
          </Tooltip>
        ) : null}
        {prefs.showLatency && latency > 0 ? (
          <Tooltip label={live ? t("chatMsg.latency.live") : t("chatMsg.latency.total")}>
            <Badge tone="outline" tabIndex={0} className="tabular-nums">
              {formatLatency(latency)}
            </Badge>
          </Tooltip>
        ) : null}
        {showCost ? (
          <Tooltip label={<BillingDetails message={message} currency={env.currency} locale={env.locale} />}>
            <Badge tone="outline" tabIndex={0}>
              {t("chatMsg.billing.badge")}
            </Badge>
          </Tooltip>
        ) : null}
        {message.editedAt ? (
          <Tooltip label={t("chatMsg.editedReplyTip")}>
            <Badge tone="outline">{t("chatMsg.edited")}</Badge>
          </Tooltip>
        ) : null}
      </div>
      <div className={cn("flex items-center gap-0.5 transition-opacity", !env.readOnly && "opacity-0 focus-within:opacity-100 group-hover/msg:opacity-100 [@media(hover:none)]:opacity-100")} data-capture-hide data-actions>
        {!env.readOnly ? (
          <>
            <IconAction label={t("chatMsg.like")} active={message.feedback === "up"} disabled={actionsOff} onClick={() => env.actions.feedback(message, "up")}>
              <ThumbsUp />
            </IconAction>
            <IconAction label={t("chatMsg.dislike")} active={message.feedback === "down"} disabled={actionsOff} onClick={() => env.actions.feedback(message, "down")}>
              <ThumbsDown />
            </IconAction>
            <IconAction label={t("chatMsg.retry")} disabled={actionsOff} onClick={() => env.actions.retry(message)}>
              <RotateCcw />
            </IconAction>
            {message.status === "interrupted" && !env.busy ? (
              <IconAction label={t("chatMsg.continue")} disabled={actionsOff} onClick={() => env.actions.continueReply(message)}>
                <Play />
              </IconAction>
            ) : null}
          </>
        ) : null}
        <IconAction label={copied ? t("chatMsg.copied") : t("chatMsg.copy")} disabled={actionsOff} onClick={async () => {
            if (!(await copy(getCopyText()))) toast.error(t("chatRich.md.copyFailed"));
          }}>
          {copied ? <Check /> : <Copy />}
        </IconAction>
        {!env.readOnly ? (
          <>
            <IconAction label={t("chatMsg.edit")} disabled={actionsOff || env.busy} onClick={onEdit}>
              <Pencil />
            </IconAction>
            {!env.temporary ? (
              <IconAction label={t("chatMsg.fork")} disabled={actionsOff || !message.persisted} onClick={() => env.actions.fork(message)}>
                <GitFork />
              </IconAction>
            ) : null}
            {message.parentId && message.persisted && !env.temporary ? (
              <IconAction label={t("chatMsg.delete")} disabled={actionsOff || env.busy} onClick={() => env.actions.remove(message)}>
                <Trash2 />
              </IconAction>
            ) : null}
            {!env.temporary && env.hasRememberApi ? <RememberPopover onSave={env.actions.rememberPreference} /> : null}
          </>
        ) : null}
        {nav && !env.temporary ? <BranchNav nav={nav} disabled={actionsOff} onCycle={(d) => env.actions.cycle(message, d)} /> : null}
        <MessageTime iso={message.createdAt} />
      </div>
      <SourcesButton sources={sources} />
    </div>
  );
}

export const AssistantMessage = memo(function AssistantMessage({ message, nav }: { message: ChatMessage; nav?: Navigator }) {
  const t = useT();
  const env = useMessageEnv();
  const live = message.live?.streaming === true;
  const pending = message.status === "pending";
  const liveThinking = useLiveThinking(message.runId);
  const [editing, setEditing] = useState(false);
  const [showCompaction, setShowCompaction] = useState(false);
  const inline = useMemo(() => detectInlineImage(message.content), [message.content]);
  const videos = message.attachments.filter(isVideoAttachment);
  const others = message.attachments.filter((a) => !isVideoAttachment(a));
  const text = useMemo(() => stripVideoPlaceholders(inline ? inline.rest : message.content), [inline, message.content]);
  const sources = useMemo(() => (live || pending ? [] : message.sources.length > 0 ? message.sources : stageCitations(message.trace)), [live, pending, message.sources, message.trace]);
  const hasAnswer = message.content.trim() !== "" || !!message.alert;
  const mediaTask = message.contentType === "image" || message.contentType === "video";

  const blocks = useBlockHostApi({
    message,
    conversationKey: env.conversationKey,
    temporary: env.temporary,
    readOnly: env.readOnly,
    busy: env.busy,
    live,
    ask: env.actions.ask,
    prefill: env.actions.prefill,
  });

  const markdownActions = useMemo(
    () => ({
      openCode: (args: Parameters<typeof env.actions.openCode>[1]) => env.actions.openCode(message, args),
      imageEditable: env.actions.imageEditable,
      editImage: env.actions.editImage,
      blocks,
    }),
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [env.actions, message.id, blocks],
  );

  // Copying gives the readable text: interactive blocks become their current state (read at click time).
  const getCopyText = () => readableMessageText(message.content, (key) => blocks?.readState(key));

  if (editing) {
    return (
      <EditBox
        initial={message.content}
        onCancel={() => setEditing(false)}
        onSave={async (value) => {
          const ok = await env.actions.saveEdit(message, value);
          if (ok) setEditing(false);
        }}
      />
    );
  }

  return (
    <div className="grid grid-cols-[minmax(0,1fr)] gap-0.5" data-role="assistant" data-run-id={message.runId}>
      {message.compaction ? (
        <button type="button" onClick={() => setShowCompaction((v) => !v)} aria-expanded={showCompaction} className="mb-1 flex items-center gap-2 text-xs text-muted-foreground" data-capture-hide>
          <span className="h-px flex-1 bg-border" />
          <span>{t("chatMsg.compacted")}</span>
          <span className="h-px flex-1 bg-border" />
        </button>
      ) : null}
      {showCompaction && message.compaction ? <p className="mb-2 rounded-lg bg-muted/50 p-2 text-xs text-muted-foreground">{message.compaction.summaryPreview}</p> : null}

      {env.prefs.showProcessTrace && message.trace?.enabled !== false && message.trace ? <ProcessSummary trace={message.trace} active={live || pending} hasAnswer={hasAnswer} /> : null}
      <AgentTrace message={message} live={liveThinking} active={live} hasAnswer={hasAnswer} />

      {pending && !message.content.trim() && !mediaTask ? <ActivityLine message={message} /> : null}
      {mediaTask && (pending || live) && message.attachments.length === 0 ? <GenerationPlaceholder message={message} /> : null}

      {inline ? (
        inline.complete ? (
          // eslint-disable-next-line @next/next/no-img-element -- inline data image from the model
          <img src={inline.src} alt={t("chatMsg.imagePreviewAlt")} className="my-2 max-h-[28rem] max-w-full rounded-xl border" />
        ) : (
          <div className="rio-shimmer my-2 aspect-[4/3] w-full max-w-md rounded-2xl" role="status" aria-label={t("chatMsg.generatingImage")} />
        )
      ) : null}

      {text.trim() !== "" ? (
        env.prefs.markdownRender ? (
          <Markdown content={text} streaming={live} scope={`m-${message.id}`} readOnly={env.readOnly} autoExpandThinking={env.prefs.autoExpandThinking} {...markdownActions} />
        ) : (
          <div className="whitespace-pre-wrap break-words text-[0.9375rem] leading-7">{text}</div>
        )
      ) : null}

      {videos.map((v) => (
        <div key={v.fileId} className="my-2 grid gap-1.5">
          <VideoPlayer a={v} />
          {env.actions.canExtendVideo(message) && !env.readOnly ? (
            <Button variant="outline" size="sm" className="w-fit" onClick={() => env.actions.extendVideo(v, message)} data-capture-hide>
              <Film /> {t("chatMsg.extendVideo")}
            </Button>
          ) : null}
        </div>
      ))}
      {mediaTask ? <GeneratedImages attachments={message.attachments} /> : null}
      {message.alert ? <AlertCard alert={message.alert} message={message.errorMessage} /> : null}
      {others.length > 0 && !mediaTask ? <AttachmentCards attachments={others as ChatAttachment[]} /> : null}
      <MetaRow message={message} nav={nav} sources={sources} onEdit={() => setEditing(true)} getCopyText={getCopyText} />
    </div>
  );
});
