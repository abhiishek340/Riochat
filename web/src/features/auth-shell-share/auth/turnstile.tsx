"use client";

import { useEffect, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { useT } from "@/lib/i18n/provider";

interface TurnstileApi {
  render: (el: HTMLElement, options: Record<string, unknown>) => string;
  reset: (id: string) => void;
  remove: (id: string) => void;
}

declare global {
  interface Window {
    turnstile?: TurnstileApi;
  }
}

const SCRIPT_URL = "https://challenges.cloudflare.com/turnstile/v0/api.js?render=explicit";
let scriptPromise: Promise<TurnstileApi> | null = null;

function loadScript(): Promise<TurnstileApi> {
  if (window.turnstile) return Promise.resolve(window.turnstile);
  if (!scriptPromise) {
    scriptPromise = new Promise<TurnstileApi>((resolve, reject) => {
      const tag = document.createElement("script");
      tag.src = SCRIPT_URL;
      tag.async = true;
      tag.onload = () => (window.turnstile ? resolve(window.turnstile) : reject(new Error("turnstile missing")));
      tag.onerror = () => reject(new Error("turnstile failed to load"));
      document.head.appendChild(tag);
    });
    scriptPromise.catch(() => {
      scriptPromise = null;
    });
  }
  return scriptPromise;
}

/**
 * Human-verification widget. `onToken("")` means "no valid token" (expired or
 * errored); bump `resetKey` to get a fresh challenge after a token was spent.
 */
export function Turnstile({ siteKey, onToken, resetKey }: { siteKey: string; onToken: (token: string) => void; resetKey: number }) {
  const t = useT();
  const host = useRef<HTMLDivElement>(null);
  const widget = useRef<string | null>(null);
  const [failed, setFailed] = useState(false);
  const [attempt, setAttempt] = useState(0);
  const callback = useRef(onToken);
  callback.current = onToken;

  useEffect(() => {
    let cancelled = false;
    const el = host.current;
    if (!el) return;
    setFailed(false);
    loadScript().then(
      (api) => {
        if (cancelled) return;
        widget.current = api.render(el, {
          sitekey: siteKey,
          callback: (token: string) => callback.current(token),
          "expired-callback": () => callback.current(""),
          "error-callback": () => {
            callback.current("");
            setFailed(true);
          },
        });
      },
      () => {
        if (cancelled) return;
        callback.current("");
        setFailed(true);
      },
    );
    return () => {
      cancelled = true;
      if (widget.current && window.turnstile) window.turnstile.remove(widget.current);
      widget.current = null;
    };
  }, [siteKey, attempt]);

  const first = useRef(true);
  useEffect(() => {
    if (first.current) {
      first.current = false;
      return;
    }
    if (widget.current && window.turnstile) window.turnstile.reset(widget.current);
    callback.current("");
  }, [resetKey]);

  return (
    <div className="grid gap-2" role="group" aria-label={t("authFlow.register.turnstileLabel")}>
      <div ref={host} className={failed ? "hidden" : "min-h-[65px]"} data-testid="turnstile" />
      {failed ? (
        <div role="alert" className="flex items-center justify-between gap-3 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive">
          <span>{t("authFlow.register.turnstileFailed")}</span>
          <Button type="button" size="sm" variant="outline" onClick={() => setAttempt((n) => n + 1)}>
            {t("authFlow.register.turnstileRetry")}
          </Button>
        </div>
      ) : null}
    </div>
  );
}
