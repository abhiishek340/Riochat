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
