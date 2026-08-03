"use client";

import { useEffect, useRef } from "react";
import { isAbortError } from "@/lib/api/errors";

/** Delay for the next poll: doubles per consecutive failure, capped at 4x. */
export function backoffDelay(baseMs: number, failures: number): number {
  return baseMs * Math.min(4, 2 ** Math.max(0, failures));
}

/**
 * Best-effort polling loop. `poll` receives an AbortSignal and resolves when
 * done; a rejection counts as a failure and stretches the next delay.
 * Pauses (aborting the in-flight call) while the tab is hidden and polls
 * straight away when it becomes visible again.
 */
export function usePolling(enabled: boolean, intervalMs: number, poll: (signal: AbortSignal) => Promise<void>): void {
  const pollRef = useRef(poll);
  useEffect(() => {
    pollRef.current = poll;
  });

  useEffect(() => {
    if (!enabled) return;
    let stopped = false;
    let timer: ReturnType<typeof setTimeout> | undefined;
    let controller: AbortController | null = null;
    let failures = 0;

    const schedule = (delay: number) => {
      if (stopped) return;
      timer = setTimeout(run, delay);
    };
    const run = async () => {
      if (stopped || document.visibilityState === "hidden") return;
      controller = new AbortController();
      try {
        await pollRef.current(controller.signal);
        failures = 0;
      } catch (error) {
        if (isAbortError(error)) return;
        failures += 1;
      } finally {
        controller = null;
      }
      schedule(backoffDelay(intervalMs, failures));
    };
    const onVisibility = () => {
      if (document.visibilityState === "hidden") {
        if (timer) clearTimeout(timer);
        controller?.abort();
      } else if (!controller) {
        if (timer) clearTimeout(timer);
        void run();
      }
    };
    document.addEventListener("visibilitychange", onVisibility);
    schedule(intervalMs);
    return () => {
      stopped = true;
      if (timer) clearTimeout(timer);
      controller?.abort();
      document.removeEventListener("visibilitychange", onVisibility);
    };
  }, [enabled, intervalMs]);
}
