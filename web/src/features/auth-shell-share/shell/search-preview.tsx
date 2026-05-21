"use client";

import { useEffect, useRef, useState } from "react";
import { MessageSquareText } from "lucide-react";
import { Button } from "@/components/ui/button";
import { Spinner } from "@/components/ui/spinner";
import { isAbortError } from "@/lib/api/errors";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { MiniMarkdown } from "../lib/mini-markdown";
import { LruCache, PREVIEW_CACHE_SIZE, PREVIEW_DEBOUNCE_MS } from "../lib/search-model";
import { fetchPreview, type PreviewMessage } from "./search-api";

const cache = new LruCache<PreviewMessage[]>(PREVIEW_CACHE_SIZE);

export function clearPreviewCache(): void {
  cache.clear();
}

type State = { kind: "hint" } | { kind: "loading" } | { kind: "failed" } | { kind: "empty" } | { kind: "ready"; messages: PreviewMessage[] };

/** Shows the tail of the hovered conversation. Fast pointer movement only fetches the last target. */
export function SearchPreview({ conversationId }: { conversationId: string | null }) {
  const t = useT();
  const [state, setState] = useState<State>({ kind: "hint" });
  const [attempt, setAttempt] = useState(0);
  const scroller = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!conversationId) {
      setState({ kind: "hint" });
      return;
    }
    const hit = cache.get(conversationId);
    if (hit) {
      setState(hit.length ? { kind: "ready", messages: hit } : { kind: "empty" });
      return;
    }
    const controller = new AbortController();
    setState({ kind: "loading" });
    const timer = setTimeout(() => {
      fetchPreview(conversationId, controller.signal).then(
        (messages) => {
          cache.set(conversationId, messages);
          setState(messages.length ? { kind: "ready", messages } : { kind: "empty" });
        },
        (error: unknown) => {
          if (!isAbortError(error)) setState({ kind: "failed" });
        },
      );
    }, PREVIEW_DEBOUNCE_MS);
    return () => {
      clearTimeout(timer);
      controller.abort();
    };
  }, [conversationId, attempt]);

  useEffect(() => {
    if (state.kind === "ready" && scroller.current) scroller.current.scrollTop = scroller.current.scrollHeight;
  }, [state]);

  const center = "flex h-full flex-col items-center justify-center gap-2 p-6 text-center text-sm text-muted-foreground";
  if (state.kind === "hint")
    return (
      <div className={center}>
        <MessageSquareText className="size-6 opacity-60" />
        {t("shellX.search.previewHint")}
      </div>
    );
  if (state.kind === "loading")
    return (
      <div className={center} role="status">
        <Spinner />
        {t("shellX.search.previewLoading")}
      </div>
    );
  if (state.kind === "failed")
    return (
      <div className={center}>
        <p>{t("shellX.search.previewFailed")}</p>
        <Button
          size="sm"
          variant="outline"
          onClick={() => {
            if (conversationId) cache.delete(conversationId);
            setAttempt((n) => n + 1);
          }}
        >
          {t("common.actions.retry")}
        </Button>
      </div>
    );
  if (state.kind === "empty") return <div className={center}>{t("shellX.search.previewEmpty")}</div>;
  return (
    <div ref={scroller} className="h-full overflow-y-auto p-4" data-testid="search-preview">
      <ul className="grid gap-4">
        {state.messages.map((m, i) => (
          <li key={m.publicID || i} className={cn("flex", m.role === "user" && "justify-end")}>
            <div
              className={cn(
                "max-w-[92%] rounded-2xl px-3 py-2 text-sm",
                m.role === "user" ? "rounded-br-md bg-primary text-primary-foreground" : "bg-muted/60",
              )}
            >
              {m.content ? <MiniMarkdown source={m.content} className="text-sm" /> : null}
              {m.errorMessage ? <p className="text-destructive">{m.errorMessage}</p> : null}
            </div>
          </li>
        ))}
      </ul>
    </div>
  );
}
