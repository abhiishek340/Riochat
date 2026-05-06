"use client";

import { useCallback, useEffect, useSyncExternalStore } from "react";
import { api } from "@/lib/api/client";
import { useSession } from "@/lib/session/provider";
import { chatPreferencesFrom, type ChatPreferences, type SettingsMap } from "./chat-settings";
import { UserSettingsStore, type SettingsSnapshot } from "./user-settings-store";

interface Wire {
  settings?: Record<string, string>;
}

const store = new UserSettingsStore({
  fetch: async () => (await api.get<Wire>("/user/settings")).settings ?? {},
  save: async (patch) => (await api.patch<Wire>("/user/settings", { settings: patch })).settings,
});

const SERVER_SNAPSHOT: SettingsSnapshot = { values: {}, loaded: false, failed: false };
let owner: string | null = null;
let loading = false;

export interface UserSettingsHandle {
  values: SettingsMap;
  loaded: boolean;
  /** The settings could not be loaded (and none are held): offer `reload`. */
  failed: boolean;
  /** Optimistic write; throws after rolling back when the server refuses. */
  update: (patch: Record<string, string>) => Promise<void>;
  /** Tries the load again; never rejects (the outcome shows in `loaded` / `failed`). */
  reload: () => Promise<void>;
}

/** Shared, live view of the signed-in user's settings (loads once per account). */
export function useUserSettings(): UserSettingsHandle {
  const { user, accessToken } = useSession();
  const snapshot = useSyncExternalStore(store.subscribe, store.getSnapshot, () => SERVER_SNAPSHOT);
  const who = accessToken && user ? user.publicID : null;

  useEffect(() => {
    if (who === null) return;
    if (owner !== who) {
      owner = who;
      store.reset();
    }
    if (store.getSnapshot().loaded || loading) return;
    loading = true;
    store.load().then(
      () => {
        loading = false;
      },
      () => {
        loading = false;
      },
    );
  }, [who]);

  const update = useCallback((patch: Record<string, string>) => store.update(patch), []);
  const reload = useCallback(() => store.load().catch(() => undefined), []);
  return { values: snapshot.values, loaded: snapshot.loaded, failed: snapshot.failed && !snapshot.loaded, update, reload };
}

/**
 * The booleans the chat feature reads. Until the settings have loaded the
 * defaults apply (everything on, except deleting files with a conversation).
 */
export function useChatPreferences(): ChatPreferences {
  const { values, loaded } = useUserSettings();
  return chatPreferencesFrom(values, loaded);
}
