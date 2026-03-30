"use client";

import { useEffect } from "react";
import { toast } from "@/components/ui/toast-store";
import { isDesktop } from "@/lib/api/platform";
import { useT } from "@/lib/i18n/provider";
import { useErrorMessage } from "@/lib/i18n/use-error-message";
import { isRecord } from "@/lib/utils/guards";
import { ignoredChannel, invoke, listen, openExternal } from "./bridge";
import { loadDistribution } from "./distribution";
import { createUpdateChecker, UPDATE_CHECK_INTERVAL_MS, UPDATE_TOAST_ID, type UpdateOffer, type UpdateSource } from "./update-check";

const source: UpdateSource = {
  checkInstallable: async () => {
    // the shell checks through its own proxy route and keeps the update by `rid`
    const raw = await invoke("check_update");
    if (!isRecord(raw) || typeof raw.version !== "string") return null;
    return { rid: typeof raw.rid === "number" ? raw.rid : 0, version: raw.version };
  },
  checkNotice: async () => {
    const raw = await invoke("check_update_notice");
    return isRecord(raw) && typeof raw.version === "string" ? { version: raw.version } : null;
  },
};

/** Checks at launch and every four hours; renders nothing. */
export function UpdateNotifier() {
  const t = useT();
  const errorMessage = useErrorMessage();

  useEffect(() => {
    if (!isDesktop()) return;
    const check = createUpdateChecker(source);
    let live = true;

    const install = async (offer: UpdateOffer) => {
      toast.loading(t("desktopSetup.update.downloading"), { id: UPDATE_TOAST_ID });
      try {
        await invoke("plugin:updater|download_and_install", { rid: offer.rid, onEvent: ignoredChannel() });
        toast.success(t("desktopSetup.update.installed"), {
          id: UPDATE_TOAST_ID,
          duration: 0,
          action: { label: t("desktopSetup.update.relaunch"), onClick: () => void invoke("plugin:process|restart").catch(() => undefined) },
        });
      } catch (error) {
        const detail = typeof error === "string" ? error : errorMessage(error);
        toast.error(t("desktopSetup.update.failed"), { id: UPDATE_TOAST_ID, description: detail });
      }
    };

    const announce = (offer: UpdateOffer) => {
      if (offer.kind === "install") {
        toast.info(t("desktopSetup.update.available", { version: offer.version }), {
          id: UPDATE_TOAST_ID,
          duration: 0,
          action: { label: t("desktopSetup.update.install"), onClick: () => void install(offer) },
        });
      } else {
        toast.info(t("desktopSetup.update.available", { version: offer.version }), {
          id: UPDATE_TOAST_ID,
          duration: 0,
          action: {
            label: t("desktopSetup.update.download"),
            onClick: () => {
              void openExternal(offer.releasesUrl ?? "").then((ok) => {
                if (!ok) toast.error(t("desktopSetup.update.openFailed"));
              });
            },
          },
        });
      }
    };

    const run = async () => {
      const distribution = await loadDistribution();
      const offer = await check(distribution);
      if (live && offer) announce(offer);
    };
    void run();
    const timer = setInterval(() => void run(), UPDATE_CHECK_INTERVAL_MS);
    return () => {
      live = false;
      clearInterval(timer);
    };
  }, [t, errorMessage]);

  return null;
}

/** The shell saves downloads straight to the Downloads folder and reports the outcome. */
export function DownloadNotifier() {
  const t = useT();
  useEffect(() => {
    if (!isDesktop()) return;
    let off: (() => void) | undefined;
    let live = true;
    void listen("downloads:finished", (payload) => {
      if (!isRecord(payload)) return;
      const fileName = typeof payload.fileName === "string" ? payload.fileName : "";
      if (payload.success === true) toast.success(t("desktopSetup.download.saved", { fileName }));
      else toast.error(t("desktopSetup.download.failed", { fileName }));
    }).then((unlisten) => {
      if (live) off = unlisten;
      else unlisten();
    });
    return () => {
      live = false;
      off?.();
    };
  }, [t]);
  return null;
}

export function DesktopNotifiers() {
  return (
    <>
      <UpdateNotifier />
      <DownloadNotifier />
    </>
  );
}
