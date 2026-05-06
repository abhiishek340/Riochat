"use client";

import { useEffect, useRef } from "react";
import { createVersionWatcher, VERSION_CHECK_INTERVAL_MS } from "@/features/auth-shell-share";
import { buildIdOf, fetchVersion } from "@/lib/api/endpoints/public";
import { isDesktop } from "@/lib/api/platform";
import { toast } from "@/components/ui/toast-store";
import { useBranding } from "@/lib/branding/provider";
import { useT } from "@/lib/i18n/provider";

const TOAST_ID = "app-version-update";

/**
 * The backend serves this very bundle, so a changed build id between two
 * checks means the server was upgraded under a running tab. Checks run on
 * mount, every ten minutes, and when the tab regains focus or becomes visible
 * (later checks are throttled to one per ten minutes). The desktop app ships its
 * own bundle and never checks. Failures are silent.
 */
export function AppVersionGuard() {
  const t = useT();
  const { branding } = useBranding();
  const message = t("common.version.updateAvailable", { brand: branding.title });
  const reload = t("common.actions.reload");
  // The wording can change (language, branding) without the remembered build being forgotten.
  const wording = useRef({ message, reload });
  wording.current = { message, reload };

  useEffect(() => {
    if (isDesktop()) return;
    const check = createVersionWatcher({
      fetchId: async () => buildIdOf(await fetchVersion()),
      onChanged: () => {
        const { message: text, reload: label } = wording.current;
        toast.info(text, { id: TOAST_ID, duration: 0, action: { label, onClick: () => window.location.reload() } });
      },
    });
    void check(true);
    const interval = setInterval(() => void check(), VERSION_CHECK_INTERVAL_MS);
    const onVisible = () => document.visibilityState === "visible" && void check();
    window.addEventListener("focus", onVisible);
    document.addEventListener("visibilitychange", onVisible);
    return () => {
      clearInterval(interval);
      window.removeEventListener("focus", onVisible);
      document.removeEventListener("visibilitychange", onVisible);
    };
  }, []);

  return null;
}
