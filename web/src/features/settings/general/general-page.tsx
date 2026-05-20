"use client";

import { usePageTitle } from "@/components/shell/header-context";
import { useT } from "@/lib/i18n/provider";
import { SettingsPage } from "../parts";
import { AppearanceSection } from "./appearance-section";
import { NotificationsSection } from "./notifications-section";
import { ProfileSection } from "./profile-section";

export function GeneralSettingsPage() {
  const t = useT();
  usePageTitle(t("settings.title"));
  return (
    <SettingsPage title={t("settings.nav.general")} description={t("settings.general.description")}>
      <ProfileSection />
      <NotificationsSection />
      <AppearanceSection />
    </SettingsPage>
  );
}
