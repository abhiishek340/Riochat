"use client";

import Link from "next/link";
import { useEffect, useState } from "react";
import { Check, CreditCard, Download, Languages, Megaphone } from "lucide-react";
import {
  DropdownMenuItem,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
} from "@/components/ui/dropdown-menu";
import { api } from "@/lib/api/client";
import { isDesktop } from "@/lib/api/platform";
import { useCapabilities } from "@/lib/capabilities/provider";
import { LOCALES, LOCALE_LABELS, type Locale } from "@/lib/i18n/config";
import { useI18n } from "@/lib/i18n/provider";
import { publishUserUpdate, useSession } from "@/lib/session/provider";
import { useFeaturePolicy } from "@/lib/session/feature-policy";
import type { AuthUser } from "@/lib/api/types";
import { isRecord } from "@/lib/utils/guards";
import { ANNOUNCEMENTS_UNREAD_EVENT, currentUnread, requestOpenAnnouncements } from "../lib/announcements-model";
import { validDownloadUrl } from "../lib/download-url";

function useUnreadAnnouncements(): boolean {
  const [unread, setUnread] = useState(false);
  useEffect(() => {
    setUnread(currentUnread());
    const onChange = (event: Event) => setUnread((event as CustomEvent<boolean>).detail === true);
    window.addEventListener(ANNOUNCEMENTS_UNREAD_EVENT, onChange);
    return () => window.removeEventListener(ANNOUNCEMENTS_UNREAD_EVENT, onChange);
  }, []);
  return unread;
}

/** Opens the announcements dialog; a dot marks unread items. Hidden when the server has the feature off. */
export function AnnouncementsMenuItem() {
  const { t } = useI18n();
  const { flags } = useCapabilities();
  const unread = useUnreadAnnouncements();
  if (!flags.announcements) return null;
  return (
    <DropdownMenuItem onSelect={() => requestOpenAnnouncements()}>
      <Megaphone />
      {t("shellX.menu.announcements")}
      {unread ? <span role="img" aria-label={t("shellX.menu.unread")} className="ml-auto size-2 rounded-full bg-destructive" /> : null}
    </DropdownMenuItem>
  );
}

export function UpgradePlanMenuItem() {
  const { t } = useI18n();
  const { flags } = useCapabilities();
  if (!flags.billingGating) return null;
  return (
    <DropdownMenuItem asChild>
      <Link href="/settings/subscription">
        <CreditCard />
        {t("shellX.menu.upgrade")}
      </Link>
    </DropdownMenuItem>
  );
}

/**
 * Only for ordinary browsers, once the admin policy has loaded, and only for a
 * plain http(s) address: the link is operator-supplied.
 */
export function DownloadDesktopMenuItem() {
  const { t } = useI18n();
  const { flags } = useCapabilities();
  const { policy, loaded } = useFeaturePolicy();
  const [browser, setBrowser] = useState(false);
  useEffect(() => setBrowser(!isDesktop()), []);
  const url = validDownloadUrl(policy.desktopDownloadURL);
  if (!browser || !loaded || !flags.desktopDownload || !policy.desktopDownloadEnabled || !url) return null;
  return (
    <DropdownMenuItem asChild>
      <a href={url} target="_blank" rel="noopener noreferrer">
        <Download />
        {t("shellX.menu.download")}
      </a>
    </DropdownMenuItem>
  );
}

/** Switches the interface language at once, then remembers it on the account (best effort). */
export function LanguageSubmenu() {
  const { t, locale, setLocale } = useI18n();
  const { accessToken } = useSession();
  const [saving, setSaving] = useState<Locale | null>(null);

  const choose = async (next: Locale) => {
    if (next === locale) return;
    await setLocale(next);
    if (!accessToken) return;
    setSaving(next);
    try {
      const data = await api.patch<unknown>("/me", { locale: next });
      if (isRecord(data) && isRecord(data.user)) publishUserUpdate(data.user as AuthUser);
    } catch {
      /* the local choice stays even if the account could not be updated */
    } finally {
      setSaving(null);
    }
  };

  return (
    <DropdownMenuSub>
      <DropdownMenuSubTrigger>
        <Languages />
        {t("shellX.menu.language")}
      </DropdownMenuSubTrigger>
      <DropdownMenuSubContent>
        {LOCALES.map((l) => (
          <DropdownMenuItem key={l} lang={l} disabled={saving === l} onSelect={() => void choose(l)}>
            {LOCALE_LABELS[l]}
            {l === locale ? <Check className="ml-auto !text-foreground" /> : null}
          </DropdownMenuItem>
        ))}
      </DropdownMenuSubContent>
    </DropdownMenuSub>
  );
}
