"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { copyText } from "../lib/clipboard";

/** `copy(text)` resolves to success and flips `copied` for 1.5 seconds. */
export function useCopy(): { copied: boolean; copy: (text: string) => Promise<boolean> } {
  const [copied, setCopied] = useState(false);
  const timer = useRef<ReturnType<typeof setTimeout> | null>(null);
  useEffect(
    () => () => {
      if (timer.current) clearTimeout(timer.current);
    },
    [],
  );
  const copy = useCallback(async (text: string) => {
    const ok = await copyText(text);
    if (ok) {
      setCopied(true);
      if (timer.current) clearTimeout(timer.current);
      timer.current = setTimeout(() => setCopied(false), 1500);
    }
    return ok;
  }, []);
  return { copied, copy };
}
