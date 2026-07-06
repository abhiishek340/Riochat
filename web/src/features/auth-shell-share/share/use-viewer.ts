"use client";

import { useEffect, useState, useSyncExternalStore } from "react";
import { resolveAccessToken } from "@/lib/api/client";
import { sessionStore } from "@/lib/api/session-store";

const subscribe = (cb: () => void) => sessionStore.subscribe(cb);
const snapshot = () => sessionStore.token();
const serverSnapshot = () => "";

/**
 * Is a person signed in? The share page can be opened without the app's
 * providers having a session yet, so besides the in-memory token this asks the
 * refresh cookie once on mount. `resolved` flips when that answer is in.
 */
export function useViewer(): { signedIn: boolean; resolved: boolean } {
  const token = useSyncExternalStore(subscribe, snapshot, serverSnapshot);
  const [resolved, setResolved] = useState(() => sessionStore.token() !== "");
  useEffect(() => {
    if (sessionStore.token()) {
      setResolved(true);
      return;
    }
    let live = true;
    void resolveAccessToken().then(() => live && setResolved(true));
    return () => {
      live = false;
    };
  }, []);
  return { signedIn: token !== "", resolved: resolved || token !== "" };
}
