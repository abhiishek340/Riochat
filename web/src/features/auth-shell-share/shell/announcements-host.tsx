"use client";

import { useCallback, useEffect, useRef, useState } from "react";
import { usePathname } from "next/navigation";
import { toast } from "@/components/ui/toast-store";
import { useCapabilities } from "@/lib/capabilities/provider";
import { useT } from "@/lib/i18n/provider";
import { useSession } from "@/lib/session/provider";
import {
  announcementsSuppressed,
  broadcastUnread,
  isUnread,
  OPEN_ANNOUNCEMENTS_EVENT,
  type Announcement,
  type AnnouncementSort,
} from "../lib/announcements-model";
import { AnnouncementsDialog } from "./announcements-dialog";
import { applyToAll, closeAnnouncement, dismissForToday, loadAnnouncements } from "./announcements-api";

type Mode = "closed" | "auto" | "manual";

/**
 * Owns the announcement queue. It loads (and may auto-open) the dialog when the
 * session or the route changes, and opens it on demand when the user menu
 * dispatches the global event. Renders only the dialog.
 */
export function AnnouncementsHost() {
  const t = useT();
  const pathname = usePathname();
  const { accessToken, user, status } = useSession();
  const { flags } = useCapabilities();

  const [mode, setMode] = useState<Mode>("closed");
  const [items, setItems] = useState<Announcement[]>([]);
  const [loading, setLoading] = useState(false);
  const [saving, setSaving] = useState(false);
  const [sort, setSort] = useState<AnnouncementSort>("default");
  const request = useRef(0);
  const modeRef = useRef<Mode>("closed");
  modeRef.current = mode;

  const initialRequired = user?.initialSecurityRequired ?? true;
  const active = flags.announcements && status === "ready" && accessToken !== "" && !initialRequired && !announcementsSuppressed(pathname);

  const reset = useCallback(() => {
    request.current += 1;
    broadcastUnread(false);
    setItems([]);
    setMode("closed");
    setLoading(false);
  }, []);

  // Auto queue: whenever who/where we are changes.
  useEffect(() => {
    if (!active) {
      reset();
      return;
    }
    if (modeRef.current === "manual") return;
    const mine = ++request.current;
    loadAnnouncements(false).then(
      (list) => {
        if (mine !== request.current) return;
        setItems(list);
        const unread = list.some(isUnread);
        setMode(unread ? "auto" : "closed");
        broadcastUnread(unread);
      },
      () => {
        if (mine !== request.current) return;
        setItems([]);
        setMode("closed");
        broadcastUnread(false);
      },
    );
  }, [active, accessToken, pathname, reset]);

  // Manual open (user menu).
  useEffect(() => {
    if (!active) return;
    const open = () => {
      const mine = ++request.current;
      setMode("manual");
      setSort("default");
      setItems([]);
      setLoading(true);
      loadAnnouncements(true).then(
        (list) => {
          if (mine !== request.current) return;
          setItems(list);
          setLoading(false);
        },
        () => {
          if (mine !== request.current) return;
          setItems([]);
          setLoading(false);
          toast.error(t("announcements.openFailed"));
        },
      );
    };
    window.addEventListener(OPEN_ANNOUNCEMENTS_EVENT, open);
    return () => window.removeEventListener(OPEN_ANNOUNCEMENTS_EVENT, open);
  }, [active, t]);

  const finish = useCallback(() => {
    setMode("closed");
    broadcastUnread(false);
  }, []);

  const run = useCallback(
    async (targets: Announcement[], action: (item: Announcement) => Promise<void>, failure: string) => {
      setSaving(true);
      try {
        const { failed, succeeded } = await applyToAll(targets, action);
        // Whatever the server accepted is read now, even when a sibling request failed.
        if (succeeded.length > 0) {
          const handled = new Set(succeeded.map((x) => x.id));
          const stamp = new Date().toISOString();
          setItems((current) => current.map((item) => (handled.has(item.id) ? { ...item, closedAt: item.closedAt ?? stamp } : item)));
        }
        if (failed > 0) {
          toast.error(failure);
          return;
        }
        finish();
      } finally {
        setSaving(false);
      }
    },
    [finish],
  );

  const unread = items.filter(isUnread);
  return (
    <AnnouncementsDialog
      open={mode !== "closed"}
      mode={mode === "manual" ? "manual" : "auto"}
      items={items}
      loading={loading}
      saving={saving}
      sort={sort}
      onSortChange={setSort}
      onHide={() => setMode("closed")}
      onDismissToday={() => void run(unread, dismissForToday, t("announcements.dismissFailed"))}
      onClose={() => {
        if (unread.length === 0) setMode("closed");
        else void run(unread, closeAnnouncement, t("announcements.closeFailed"));
      }}
    />
  );
}
