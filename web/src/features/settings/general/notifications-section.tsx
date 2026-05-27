"use client";

import { useEffect, useState } from "react";
import { Switch } from "@/components/ui/switch";
import { toast } from "@/components/ui/toast-store";
import { useT } from "@/lib/i18n/provider";
import { SettingRow, SettingsSection } from "../parts";
import {
  currentPermission,
  notificationsSupported,
  readCompletionNotificationsEnabled,
  requestPermission,
  writeCompletionNotificationsEnabled,
  type NotificationPermissionState,
} from "../lib/notifications";

export function NotificationsSection() {
  const t = useT();
  const [hydrated, setHydrated] = useState(false);
  const [enabled, setEnabled] = useState(false);
  const [permission, setPermission] = useState<NotificationPermissionState>("default");
  const [busy, setBusy] = useState(false);

  useEffect(() => {
    setEnabled(readCompletionNotificationsEnabled());
    setPermission(currentPermission());
    setHydrated(true);
  }, []);

  const supported = hydrated && notificationsSupported();

  const toggle = async (next: boolean) => {
    if (busy) return;
    if (!next) {
      writeCompletionNotificationsEnabled(false);
      setEnabled(false);
      setPermission(currentPermission());
      return;
    }
    setBusy(true);
    try {
      const result = await requestPermission();
      setPermission(result);
      const on = result === "granted";
      writeCompletionNotificationsEnabled(on);
      setEnabled(on);
      if (result === "unsupported") toast.error(t("settings.general.notifications.toastUnsupported"));
      else if (result === "denied") toast.error(t("settings.general.notifications.toastDenied"));
      else if (on) toast.success(t("settings.general.notifications.toastEnabled"));
    } finally {
      setBusy(false);
    }
  };

  const help = !hydrated || permission === "default" ? "help" : permission === "unsupported" ? "helpUnsupported" : permission === "denied" ? "helpDenied" : "helpGranted";

  return (
    <SettingsSection title={t("settings.general.notifications.title")}>
      <SettingRow title={t("settings.general.notifications.completion")} description={t(`settings.general.notifications.${help}`)} htmlFor="completion-notify">
        <Switch id="completion-notify" checked={enabled && permission === "granted"} disabled={!supported || busy} onCheckedChange={(v) => void toggle(v)} />
      </SettingRow>
    </SettingsSection>
  );
}
