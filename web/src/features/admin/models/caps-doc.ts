"use client";

import { useCallback, useMemo } from "react";
import { parseCaps, type Caps } from "./capabilities";

/** Editable text of a capabilities object: empty stays empty, anything else is pretty printed. */
export function prettyCaps(caps: Caps): string {
  return Object.keys(caps).length === 0 ? "" : JSON.stringify(caps, null, 2);
}

/**
 * The capabilities text as an object. While the text is not a JSON object
 * every visual edit is refused (`valid` is false) so nothing is overwritten.
 */
export function useCapsDoc(text: string, setText: (text: string) => void) {
  const parsed = useMemo(() => parseCaps(text), [text]);
  const caps: Caps = parsed.ok ? parsed.value : {};
  const valid = parsed.ok;
  const update = useCallback(
    (change: (caps: Caps) => Caps) => {
      if (!parsed.ok) return false;
      setText(prettyCaps(change(parsed.value)));
      return true;
    },
    [parsed, setText],
  );
  return { caps, valid, update };
}

export type CapsDoc = ReturnType<typeof useCapsDoc>;
