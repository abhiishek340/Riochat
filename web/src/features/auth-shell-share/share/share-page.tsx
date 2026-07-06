"use client";

import { useEffect, useMemo, useState } from "react";
import { useRouter, useSearchParams } from "next/navigation";
import { ChevronLeft, ChevronRight, FileText, Link2Off, Paperclip } from "lucide-react";
import { AppLogo } from "@/components/brand/logo";
import { Button } from "@/components/ui/button";
import { EmptyState } from "@/components/ui/empty-state";
import { Skeleton } from "@/components/ui/skeleton";
import { toast } from "@/components/ui/toast-store";
import { absolutizeApiPath } from "@/lib/api/base-url";
import { useI18n } from "@/lib/i18n/provider";
import { isApiError } from "@/lib/api/errors";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { cn } from "@/lib/utils/cn";
import { MiniMarkdown } from "../lib/mini-markdown";
import {
  buildBranchIndex,
  defaultSelection,
  loginForShare,
  parentKey,
  rewriteSharedFileUrls,
  sharedFileUrl,
  stepSibling,
  visiblePath,
  type ShareAttachment,
  type ShareMessage,
  type ShareSnapshot,
  type PathEntry,
} from "../lib/share-model";
import { cloneSharedConversation, fetchSharedConversation } from "./api";
import { formatBytes, formatCount, formatLatency, formatShareDate } from "./format";
import { useViewer } from "./use-viewer";

type Load = { state: "loading" } | { state: "ready"; snapshot: ShareSnapshot } | { state: "unavailable"; error: unknown };

/** `/share?conversation_id=<shareId>`: a read-only snapshot with a way to continue it as your own chat. */
export function SharePage() {
  const params = useSearchParams();
  const shareId = (params.get("conversation_id") ?? "").trim();
  // Keyed so switching to another share starts from clean branch selections.
  return <ShareView key={shareId} shareId={shareId} />;
}

function ShareView({ shareId }: { shareId: string }) {
  const { t } = useI18n();
  const errorMessage = useErrorMessage();
  const [load, setLoad] = useState<Load>(shareId ? { state: "loading" } : { state: "unavailable", error: null });
  useEffect(() => {
    if (!shareId) return;
    const controller = new AbortController();
    fetchSharedConversation(shareId, controller.signal).then(
      (snapshot) => {
        if (!controller.signal.aborted) setLoad({ state: "ready", snapshot });
      },
      (error: unknown) => {
        if (controller.signal.aborted) return;
        setLoad({ state: "unavailable", error });
      },
    );
    return () => controller.abort();
  }, [shareId]);

  if (load.state === "loading") return <ShareSkeleton />;
  if (load.state === "unavailable") {
    return (
      <div className="mx-auto flex min-h-[60dvh] max-w-xl items-center justify-center px-4">
        <EmptyState icon={<Link2Off />} title={t("sharePage.unavailable.title")} description={unavailableText(load.error, errorMessage, t("sharePage.unavailable.body"))} />
      </div>
    );
  }
  return <Snapshot snapshot={load.snapshot} />;
}

/**
 * A missing, revoked or malformed share all come back as 4xx with an English
 * server sentence; the localized explanation says the same thing better.
 * Anything else (network, 5xx, a rate limit) keeps the server's own wording.
 */
function unavailableText(error: unknown, format: (error: unknown, fallback?: string) => string, fallback: string): string {
  if (error === null || error === undefined) return fallback;
  if (isApiError(error) && (error.status === 400 || error.status === 404 || error.status === 410)) return fallback;
  return format(error, fallback);
}

function ShareSkeleton() {
  const { t } = useI18n();
  return (
    <div role="status" aria-label={t("common.states.loading")} className="mx-auto grid max-w-3xl gap-6 px-4 py-10">
      <Skeleton className="h-8 w-2/3" />
      <Skeleton className="h-4 w-1/3" />
      <Skeleton className="ml-auto h-12 w-1/2 rounded-2xl" />
      <Skeleton className="h-28 w-full rounded-2xl" />
      <Skeleton className="ml-auto h-12 w-2/5 rounded-2xl" />
    </div>
  );
}

function Snapshot({ snapshot }: { snapshot: ShareSnapshot }) {
  const { t, locale } = useI18n();
  const index = useMemo(() => buildBranchIndex(snapshot.messages), [snapshot.messages]);
  const [selection, setSelection] = useState<Map<string, string>>(() => defaultSelection(index, snapshot.defaultIds));
  const path = useMemo(() => visiblePath(index, selection), [index, selection]);
  const created = formatShareDate(snapshot.createdAt, locale);

  const move = (entry: PathEntry, delta: -1 | 1) => {
    const target = entry.siblings[stepSibling(entry.position, delta, entry.siblings.length)];
    if (!target || target.id === entry.message.id) return;
    setSelection((current) => new Map(current).set(parentKey(index, entry.message), target.id));
  };

  return (
    <div className="mx-auto max-w-3xl px-4 pb-40 pt-8 sm:px-6">
      <header className="mb-8 grid gap-1.5">
        <h1 className="text-balance text-2xl font-semibold tracking-tight">{snapshot.title || t("sharePage.untitled")}</h1>
        <p className="text-sm text-muted-foreground">
          {created ? `${created} · ` : ""}
          {t("sharePage.count", { count: snapshot.messages.length })}
        </p>
      </header>

      <ol className="grid gap-7">
        {path.map((entry) => (
          <li key={entry.message.id}>
            <MessageView entry={entry} shareId={snapshot.shareId} onMove={(delta) => move(entry, delta)} />
          </li>
        ))}
      </ol>

      <footer className="mt-16 flex justify-center opacity-40">
        <AppLogo className="size-7" />
      </footer>
      <ContinueButton shareId={snapshot.shareId} />
    </div>
  );
}

function BranchNav({ entry, onMove }: { entry: PathEntry; onMove: (delta: -1 | 1) => void }) {
  const { t } = useI18n();
  const total = entry.siblings.length;
  if (total < 2) return null;
  return (
    <div className="inline-flex items-center gap-0.5 text-xs text-muted-foreground" role="group" aria-label={t("sharePage.branch.label")}>
      <Button variant="ghost" size="icon-xs" aria-label={t("sharePage.branch.previous")} disabled={entry.position === 0} onClick={() => onMove(-1)}>
        <ChevronLeft />
      </Button>
      <span aria-live="polite" className="min-w-10 text-center tabular-nums">
        {entry.position + 1} / {total}
      </span>
      <Button variant="ghost" size="icon-xs" aria-label={t("sharePage.branch.next")} disabled={entry.position === total - 1} onClick={() => onMove(1)}>
        <ChevronRight />
      </Button>
    </div>
  );
}

function MessageView({ entry, shareId, onMove }: { entry: PathEntry; shareId: string; onMove: (delta: -1 | 1) => void }) {
  const { t, locale } = useI18n();
  const message = entry.message;
  const text = useMemo(() => rewriteSharedFileUrls(message.content, shareId), [message.content, shareId]);

  const blocked = message.status === "blocked";

  if (message.role === "user") {
    return (
      <article className="grid justify-items-end gap-1.5">
        {blocked ? (
          <p className="max-w-[85%] rounded-2xl border border-dashed px-4 py-2.5 text-sm italic text-muted-foreground">{t("sharePage.blocked")}</p>
        ) : text ? (
          <div className="max-w-[85%] whitespace-pre-wrap break-words rounded-2xl rounded-br-md bg-primary px-4 py-2.5 text-[15px] leading-relaxed text-primary-foreground">
            {text}
          </div>
        ) : null}
        <Attachments items={message.attachments} shareId={shareId} align="end" />
        <BranchNav entry={entry} onMove={onMove} />
      </article>
    );
  }

  if (message.role !== "assistant") {
    return (
      <article className="rounded-xl border border-dashed px-4 py-3 text-sm text-muted-foreground">
        <span className="mb-1 block text-xs font-semibold uppercase tracking-wide">{t("sharePage.systemRole")}</span>
        <MiniMarkdown source={text} className="text-sm" />
      </article>
    );
  }

  const latency = formatLatency(message.latencyMs);
  const tokens =
    message.inputTokens + message.outputTokens > 0
      ? t("sharePage.meta.tokens", { input: formatCount(message.inputTokens, locale), output: formatCount(message.outputTokens, locale) })
      : "";
  const meta = [message.modelName, latency, tokens].filter(Boolean);
  return (
    <article className="grid gap-2">
      {blocked ? <p className="rounded-lg border border-dashed px-3 py-2 text-sm italic text-muted-foreground">{t("sharePage.blocked")}</p> : null}
      {!blocked && text ? <MiniMarkdown source={text} /> : null}
      {message.status === "error" || message.errorMessage ? (
        <p role="alert" className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          {message.errorMessage || t("sharePage.replyFailed")}
        </p>
      ) : message.status === "interrupted" || message.status === "canceled" ? (
        <p className="text-sm italic text-muted-foreground">{t("sharePage.replyFailed")}</p>
      ) : null}
      <Attachments items={message.attachments} shareId={shareId} align="start" />
      <div className="flex flex-wrap items-center gap-x-3 gap-y-1">
        <BranchNav entry={entry} onMove={onMove} />
        {meta.length ? <p className="text-xs text-muted-foreground">{meta.join(" · ")}</p> : null}
      </div>
    </article>
  );
}

function isImage(item: ShareAttachment): boolean {
  return item.kind === "image" || item.mime.startsWith("image/");
}

function Attachments({ items, shareId, align }: { items: ShareMessage["attachments"]; shareId: string; align: "start" | "end" }) {
  if (items.length === 0) return null;
  return (
    <ul className={cn("flex max-w-[85%] flex-wrap gap-2", align === "end" && "justify-end")}>
      {items.map((item) => {
        const href = absolutizeApiPath(sharedFileUrl(shareId, item.fileId));
        return (
          <li key={item.fileId}>
            {isImage(item) ? (
              <a href={href} target="_blank" rel="noopener noreferrer" className="block overflow-hidden rounded-xl border">
                {/* eslint-disable-next-line @next/next/no-img-element -- share-scoped file endpoint */}
                <img src={href} alt={item.name} loading="lazy" className="max-h-56 max-w-full object-cover" />
              </a>
            ) : (
              <a
                href={href}
                target="_blank"
                rel="noopener noreferrer"
                className="inline-flex max-w-64 items-center gap-2 rounded-lg border bg-card px-3 py-2 text-sm hover:bg-muted"
              >
                {item.kind === "document" ? <FileText className="size-4 shrink-0" /> : <Paperclip className="size-4 shrink-0" />}
                <span className="truncate">{item.name}</span>
                {item.size ? <span className="shrink-0 text-xs text-muted-foreground">{formatBytes(item.size)}</span> : null}
              </a>
            )}
          </li>
        );
      })}
    </ul>
  );
}

function ContinueButton({ shareId }: { shareId: string }) {
  const { t } = useI18n();
  const errorMessage = useErrorMessage();
  const router = useRouter();
  const viewer = useViewer();
  const [busy, setBusy] = useState(false);

  const onClick = async () => {
    if (!shareId || busy) return;
    if (!viewer.signedIn) {
      router.push(loginForShare(shareId));
      return;
    }
    setBusy(true);
    try {
      const id = await cloneSharedConversation(shareId);
      router.push(`/chat?conversation_id=${encodeURIComponent(id)}`);
    } catch (error) {
      toast.error(t("sharePage.cta.failed"), { description: errorMessage(error) });
      setBusy(false);
    }
  };

  const label = !viewer.signedIn ? t("sharePage.cta.signIn") : busy ? t("sharePage.cta.continuing") : t("sharePage.cta.continue");
  return (
    <div className="pointer-events-none fixed inset-x-0 bottom-6 z-30 flex justify-center px-4">
      <Button size="lg" className="pointer-events-auto rounded-full px-7 shadow-lg" onClick={() => void onClick()} loading={busy} disabled={!viewer.resolved}>
        {label}
      </Button>
    </div>
  );
}
