"use client";

import { useRouter } from "next/navigation";
import { useEffect, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { useCapabilities } from "@/lib/capabilities/provider";
import { useT } from "@/lib/i18n/provider";
import { readStorage, writeStorage } from "@/lib/utils/storage";
import { listProviders, loadBridge } from "./login/api";

const SEEN_KEY = "riochat:admin:bridge-notice-seen";

/**
 * Once per browser session: when providers exist but the server has no public
 * address for their callbacks, explain why sign-in through them cannot work.
 * Closing the dialog by any means counts as dismissing it.
 */
export function AdminBridgeNotice() {
  const t = useT();
  const router = useRouter();
  const { flags, loaded } = useCapabilities();
  const [open, setOpen] = useState(false);
  const enabled = loaded && flags.identityProviders;

  useEffect(() => {
    if (!enabled || readStorage(SEEN_KEY, "session")) return;
    const controller = new AbortController();
    Promise.all([listProviders(controller.signal), loadBridge(controller.signal)]).then(
      ([providers, bridge]) => {
        if (providers.length > 0 && !bridge.enabled) setOpen(true);
      },
      () => undefined, // a failed lookup is silent
    );
    return () => controller.abort();
  }, [enabled]);

  const close = (then?: () => void) => {
    writeStorage(SEEN_KEY, "1", "session");
    setOpen(false);
    then?.();
  };

  return (
    <Dialog open={open} onOpenChange={(next) => !next && close()}>
      <DialogContent>
        <DialogHeader>
          <DialogTitle>{t("adminLogin.bridge.title")}</DialogTitle>
          <DialogDescription>{t("adminLogin.bridge.body")}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={() => close()}>
            {t("adminLogin.bridge.dismiss")}
          </Button>
          <Button onClick={() => close(() => router.push("/admin/login"))}>{t("adminLogin.bridge.openSettings")}</Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
