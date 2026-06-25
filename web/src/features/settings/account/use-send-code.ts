"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { toast } from "@/components/ui/toast-store";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { useT, type MessageKey } from "@/lib/i18n/provider";
import { secondsLeft } from "../lib/policy";
import type { SendResult } from "./api";

export const RESEND_SECONDS = 60;

export interface Cooldown {
  left: number;
  active: boolean;
  start: () => void;
  reset: () => void;
}

/** Counts down from 60 once started; `left` is whole seconds remaining. */
export function useCooldown(seconds = RESEND_SECONDS): Cooldown {
  const [until, setUntil] = useState(0);
  const [now, setNow] = useState(() => Date.now());
  useEffect(() => {
    if (until <= Date.now()) return;
    setNow(Date.now());
    const id = setInterval(() => {
      const t = Date.now();
      setNow(t);
      if (t >= until) clearInterval(id);
    }, 500);
    return () => clearInterval(id);
  }, [until]);
  const left = secondsLeft(until, now);
  const start = useCallback(() => {
    const t = Date.now();
    setNow(t);
    setUntil(t + seconds * 1000);
  }, [seconds]);
  const reset = useCallback(() => setUntil(0), []);
  return { left, active: left > 0, start, reset };
}

export interface CodeSender {
  sending: boolean;
  cooldown: Cooldown;
  debugCode: string;
  /** True once a code has been sent. */
  sent: boolean;
  send: () => Promise<void>;
  reset: () => void;
}

/**
 * One "email me a code" button's state: ignores clicks while a send is in
 * flight or cooling down, starts the 60 s cooldown on success, surfaces the
 * debug code some servers return outside production.
 */
export function useCodeSender(request: () => Promise<SendResult>, successKey: MessageKey): CodeSender {
  const t = useT();
  const errorMessage = useErrorMessage();
  const cooldown = useCooldown();
  const [sending, setSending] = useState(false);
  const [debugCode, setDebugCode] = useState("");
  const [sent, setSent] = useState(false);
  const inFlight = useRef(false);
  const requestRef = useRef(request);
  requestRef.current = request;

  const send = useCallback(async () => {
    if (inFlight.current || cooldown.active) return;
    inFlight.current = true;
    setSending(true);
    try {
      const result = await requestRef.current();
      if (result.sent) {
        cooldown.start();
        setSent(true);
        setDebugCode(result.debugCode);
        toast.success(t(successKey));
      }
    } catch (e) {
      toast.error(t("settings.account.toasts.sendCodeFailed"), { description: errorMessage(e) });
    } finally {
      inFlight.current = false;
      setSending(false);
    }
    // `cooldown` identity changes every tick; its methods are stable.
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cooldown.active, cooldown.start, successKey, t, errorMessage]);

  const reset = useCallback(() => {
    cooldown.reset();
    setSent(false);
    setDebugCode("");
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [cooldown.reset]);

  return { sending, cooldown, debugCode, sent, send, reset };
}
