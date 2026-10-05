"use client";

import { useEffect, useState } from "react";
import { usePageTitle } from "@/components/shell/header-context";
import { AppLogo } from "@/components/brand/logo";
import { fetchVersion } from "@/lib/api/endpoints/public";
import type { VersionInfo } from "@/lib/api/types";
import { isDesktop } from "@/lib/api/platform";
import { useBranding } from "@/lib/branding/provider";
import { useT } from "@/lib/i18n/provider";
import { SettingRow, SettingsPage, SettingsSection } from "../parts";

export function AboutSettingsPage() {
  const t = useT();
  const { branding } = useBranding();
  usePageTitle(t("settings.title"));
  const [info, setInfo] = useState<VersionInfo | null>(null);
  const [desktop, setDesktop] = useState(false);

  useEffect(() => {
    setDesktop(isDesktop());
    const controller = new AbortController();
    fetchVersion(controller.signal).then(setInfo, () => undefined);
    return () => controller.abort();
  }, []);

  const dash = (v: string | undefined) => (v && v.trim() ? v : "-");

  return (
    <SettingsPage title={t("settings.nav.about")}>
      <div className="flex items-center gap-4 rounded-xl border bg-card p-5 shadow-sm">
        <AppLogo className="size-14" />
        <div className="grid min-w-0 gap-0.5">
          <h3 className="text-lg font-semibold tracking-tight">{branding.title}</h3>
          <p className="text-sm text-muted-foreground">{branding.description || t("settings.about.tagline")}</p>
          <span className="text-xs uppercase tracking-wide text-muted-foreground">{t("settings.about.console")}</span>
        </div>
      </div>
      <SettingsSection title={t("settings.about.details")}>
        <SettingRow title={t("settings.about.version")}>
          <span className="font-mono text-sm">{dash(info?.version)}</span>
        </SettingRow>
        <SettingRow title={t("settings.about.build")}>
          <span className="font-mono text-sm">{dash(info?.commit)}</span>
        </SettingRow>
        <SettingRow title={t("settings.about.builtAt")}>
          <span className="font-mono text-sm">{dash(info?.buildTime)}</span>
        </SettingRow>
        {desktop ? (
          <SettingRow title={t("settings.about.distribution")}>
            <span className="text-sm">{t("settings.about.desktopApp")}</span>
          </SettingRow>
        ) : null}
        <SettingRow title={t("settings.about.copyright")} description={t("settings.about.copyrightBody", { brand: branding.title })} />
        <SettingRow title={t("settings.about.license")} description={t("settings.about.licenseBody")} />
      </SettingsSection>
    </SettingsPage>
  );
}
