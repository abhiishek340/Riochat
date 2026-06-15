"use client";

import { memo, useCallback, useEffect, useLayoutEffect, useRef, useState, type ReactNode } from "react";
import { ArrowDown, Check, RefreshCw, TriangleAlert } from "lucide-react";
import { Button, Skeleton } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import type { ChatView } from "../state/engine-types";
import type { Navigator } from "../lib/tree";
import type { ChatMessage } from "../types";
import { AssistantMessage } from "./message/assistant-message";
import { useMessageEnv } from "./message/env";
import { UserMessage } from "./message/user-message";
import { OutlineRail, PositionRail } from "./rails";

function sameNav(a?: Navigator, b?: Navigator): boolean {
  if (a === b) return true;
  if (!a || !b) return false;
  return a.index === b.index && a.total === b.total && a.canPrevious === b.canPrevious && a.canNext === b.canNext;
}

const Row = memo(
  function Row({ message, nav, selected }: { message: ChatMessage; nav?: Navigator; selected: boolean }) {
    const env = useMessageEnv();
    const t = useT();
    const selectable = env.selection.active && message.persisted && message.status !== "pending";
    const body =
      message.role === "user" ? (
        <UserMessage message={message} nav={nav} />
      ) : message.role === "assistant" ? (
        <AssistantMessage message={message} nav={nav} />
      ) : (
        <div role="alert" className="rounded-xl border bg-muted/40 px-3 py-2 text-sm text-muted-foreground">
          {message.content}
        </div>
      );
    return (
      <div
        data-message-id={message.id}
        data-role={message.role}
        data-turn-id={message.role === "user" ? message.id : undefined}
        data-selected={selected || undefined}
        data-capture-row={selectable ? "" : undefined}
        className={cn(
          "group/msg relative py-3",
          env.selection.active && "rounded-xl pl-8 transition-colors",
          selected && "bg-accent/40",
          selectable && "cursor-pointer",
        )}
        onClick={selectable ? () => env.selection.toggle(message.id) : undefined}
      >
        {env.selection.active ? (
          <span
            role="checkbox"
            aria-checked={selected}
            aria-disabled={!selectable}
            aria-label={t("chatNav.screenshot.selectMessage")}
            tabIndex={selectable ? 0 : -1}
            onKeyDown={(e) => {
              if (selectable && (e.key === " " || e.key === "Enter")) {
                e.preventDefault();
                env.selection.toggle(message.id);
              }
            }}
            className={cn("absolute left-1.5 top-4 flex size-5 items-center justify-center rounded border", selected ? "border-primary bg-primary text-primary-foreground" : "bg-background", !selectable && "opacity-30")}
            data-capture-hide
          >
            {selected ? <Check className="size-3.5" /> : null}
          </span>
        ) : null}
        <div className={cn(env.selection.active && "pointer-events-none")}>{body}</div>
      </div>
    );
  },
  (a, b) => a.message === b.message && sameNav(a.nav, b.nav) && a.selected === b.selected,
);

export interface MessageListProps {
  view: ChatView;
  onLoadOlder: () => void;
  onReload: () => void;
  onNewChat: () => void;
  contentMaxRem: number;
  panelOpen: boolean;
  footer?: ReactNode;
  scrollerRef: React.RefObject<HTMLDivElement | null>;
  selectedIds: ReadonlySet<string>;
  loadErrorText: string;
}

export function MessageList({ view, onLoadOlder, onReload, onNewChat, contentMaxRem, panelOpen, footer, scrollerRef, selectedIds, loadErrorText }: MessageListProps) {
  const t = useT();
  const env = useMessageEnv();
  const inner = useRef<HTMLDivElement>(null);
  const stick = useRef(true);
  const [away, setAway] = useState(false);
  const anchor = useRef<{ height: number; first: string } | null>(null);
  // While we scroll on our own, intermediate positions must not end "follow" mode.
  const programmatic = useRef(0);
  const lastRun = useRef("");

  const firstId = view.path[0]?.id ?? "";
  const lastUser = [...view.path].reverse().find((m) => m.role === "user");

  const reduced = () => window.matchMedia("(prefers-reduced-motion: reduce)").matches;
  const toBottom = useCallback(
    (smooth: boolean) => {
      const el = scrollerRef.current;
      if (!el) return;
      const animated = smooth && !reduced();
      if (animated) programmatic.current = Date.now() + 800;
      el.scrollTo({ top: el.scrollHeight, behavior: animated ? "smooth" : "auto" });
    },
    [scrollerRef],
  );

  const onScroll = useCallback(() => {
    const el = scrollerRef.current;
    if (!el) return;
    const bottom = el.scrollHeight - el.scrollTop - el.clientHeight;
    if (Date.now() > programmatic.current) stick.current = bottom < 48;
    setAway(bottom > 120 && Date.now() > programmatic.current);
    if (el.scrollTop <= 48 && bottom > 48 && view.hasOlder && !view.loadingOlder) {
      anchor.current = { height: el.scrollHeight, first: firstId };
      onLoadOlder();
    }
  }, [scrollerRef, view.hasOlder, view.loadingOlder, firstId, onLoadOlder]);

  // Keep the reading position when older messages are prepended.
  useLayoutEffect(() => {
    const el = scrollerRef.current;
    const a = anchor.current;
    if (el && a && a.first !== firstId) {
      el.scrollTop += el.scrollHeight - a.height;
      anchor.current = null;
    }
  }, [firstId, scrollerRef]);

  // Short histories keep loading until the content overflows.
  useEffect(() => {
    const el = scrollerRef.current;
    if (!el || !view.hasOlder || view.loadingOlder || view.loading) return;
    if (el.scrollHeight - el.clientHeight <= 48) onLoadOlder();
  }, [view.hasOlder, view.loadingOlder, view.loading, view.path.length, scrollerRef, onLoadOlder]);

  // Follow the newest content while the user stays at the bottom.
  useEffect(() => {
    const node = inner.current;
    if (!node || typeof ResizeObserver === "undefined") return;
    const ro = new ResizeObserver(() => {
      if (stick.current) toBottom(false);
    });
    ro.observe(node);
    return () => ro.disconnect();
  }, [toBottom]);

  // A new turn scrolls smoothly to the end.
  useEffect(() => {
    const run = lastUser?.runId || lastUser?.id || "";
    if (run && run !== lastRun.current) {
      if (lastRun.current !== "") {
        stick.current = true;
        toBottom(true);
      } else {
        toBottom(false);
      }
      lastRun.current = run;
    }
  }, [lastUser?.runId, lastUser?.id, toBottom]);

  // Streaming text is not read out piece by piece; the start and the end of a reply are.
  const [announcement, setAnnouncement] = useState("");
  const wasBusy = useRef(false);
  const lastReplyStatus = [...view.path].reverse().find((m) => m.role === "assistant")?.status;
  useEffect(() => {
    if (view.busy && !wasBusy.current) setAnnouncement(t("chatMsg.thinkingStatus"));
    else if (!view.busy && wasBusy.current) setAnnouncement(lastReplyStatus === "success" ? t("chatMisc.notify.ready") : "");
    wasBusy.current = view.busy;
  }, [view.busy, lastReplyStatus, t]);

  const reset = view.key;
  useEffect(() => {
    lastRun.current = "";
    stick.current = true;
  }, [reset]);

  if (view.loading) {
    return (
      <div className="mx-auto grid w-full gap-6 px-4 py-6" style={{ maxWidth: `${contentMaxRem}rem` }} role="status" aria-busy aria-label={t("common.states.loading")}>
        <Skeleton className="ml-auto h-10 w-2/3" />
        <Skeleton className="h-24 w-full" />
        <Skeleton className="ml-auto h-10 w-1/2" />
        <Skeleton className="h-32 w-full" />
      </div>
    );
  }

  if (view.loadError && view.path.length === 0) {
    return (
      <div className="mx-auto flex h-full max-w-md flex-col items-center justify-center gap-3 px-6 text-center" role="alert">
        <TriangleAlert className="size-8 text-destructive" />
        <h2 className="text-base font-semibold">{t("chatMsg.loadError.title")}</h2>
        <p className="text-sm text-muted-foreground">{loadErrorText}</p>
        <div className="flex items-center gap-2">
          <Button variant="outline" onClick={onReload}>
            <RefreshCw /> {t("chatMsg.loadError.refresh")}
          </Button>
          <span className="text-sm text-muted-foreground">{t("chatMsg.loadError.or")}</span>
          <Button onClick={onNewChat}>{t("chatMsg.loadError.newChat")}</Button>
        </div>
      </div>
    );
  }

  const outlineSignature = `${view.path.length}:${view.path[view.path.length - 1]?.content.length ?? 0}`;
  return (
    <div className="relative min-h-0 flex-1">
      <div
        ref={scrollerRef}
        onScroll={onScroll}
        role="log"
        aria-live="off"
        aria-busy={view.busy}
        aria-label={t("chatMsg.listLabel")}
        className="absolute inset-0 overflow-y-auto overscroll-contain"
        data-message-scroller
      >
        <div ref={inner} data-message-inner className="mx-auto w-full px-4 pb-6 pt-4" style={{ maxWidth: `${contentMaxRem}rem` }}>
          {view.loadingOlder ? <Skeleton className="mb-3 h-8 w-1/2" /> : null}
          {view.path.map((m) => (
            <Row key={m.key} message={m} nav={view.navigators.get(m.id)} selected={selectedIds.has(m.id)} />
          ))}
          {footer}
        </div>
      </div>
      <div role="status" aria-live="polite" className="sr-only" data-capture-hide>
        {announcement}
      </div>
      {away ? (
        <Button
          variant="outline"
          size="icon-sm"
          onClick={() => {
            stick.current = true;
            toBottom(true);
          }}
          aria-label={t("chatMsg.scrollToBottom")}
          className="absolute bottom-3 left-1/2 z-10 -translate-x-1/2 rounded-full bg-background shadow-md"
          data-capture-hide
        >
          <ArrowDown />
        </Button>
      ) : null}
      {!env.selection.active ? <PositionRail path={view.path} scroller={scrollerRef} /> : null}
      {!env.selection.active && !panelOpen ? <OutlineRail scroller={scrollerRef} signature={outlineSignature} /> : null}
    </div>
  );
}
