"use client";

import { useCallback, useEffect, useRef, useState } from "react";

/** A visible seconds countdown (resend buttons). `start()` restarts it; `reset()` clears it. */
export function useCooldown(): { seconds: number; start: (seconds: number) => void; reset: () => void } {
  const [seconds, setSeconds] = useState(0);
  const endsAt = useRef(0);

  const start = useCallback((total: number) => {
    endsAt.current = Date.now() + total * 1000;
    setSeconds(total);
  }, []);
  const reset = useCallback(() => {
    endsAt.current = 0;
    setSeconds(0);
  }, []);

  const running = seconds > 0;
  useEffect(() => {
    if (!running) return;
    const id = setInterval(() => {
      const left = Math.max(0, Math.ceil((endsAt.current - Date.now()) / 1000));
      setSeconds(left);
    }, 1000);
    return () => clearInterval(id);
  }, [running]);

  return { seconds, start, reset };
}

export const RESEND_SECONDS = 60;
