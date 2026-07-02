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
