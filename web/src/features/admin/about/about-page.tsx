"use client";

import { ArrowUpCircle, ExternalLink, RefreshCw, ShieldCheck } from "lucide-react";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { fetchVersion } from "@/lib/api/endpoints/public";
import type { VersionInfo } from "@/lib/api/types";
import { useBranding } from "@/lib/branding/provider";
import { useT } from "@/lib/i18n/provider";
import { useFeaturePolicy } from "@/lib/session/feature-policy";
import { AdminPage, Mono, Section } from "../components/page";
import { SettingsGroup } from "../components/settings-group";
import { AdminSection } from "../components/section-guard";
import type { FieldDef } from "../lib/settings-form";
import { boolOf, fieldId } from "../lib/settings-form";
import { useSettingsForm } from "../lib/use-settings-form";
import {
  cacheRelease,
  checkLatestRelease,
  isUpdateAvailable,
  LATEST_RELEASE_EVENT,
  readCachedRelease,
  withV,
  type CheckOutcome,
  type LatestRelease,
} from "../lib/update-check";

export const DEFAULT_DOWNLOAD_URL = "https://github.com/riochat/riochat/releases/latest";

const FIELDS: FieldDef[] = [
  { ns: "desktop", key: "download_enabled", type: "bool", label: "adminAbout.desktop.enabled", hint: "adminAbout.desktop.enabledHint", def: "true", blankDefault: true },
  { ns: "desktop", key: "download_url", type: "string", label: "adminAbout.desktop.url", hint: "adminAbout.desktop.urlHint", def: DEFAULT_DOWNLOAD_URL, blankDefault: true },
];
const ENABLED = fieldId(FIELDS[0] as FieldDef);
const URL_ID = fieldId(FIELDS[1] as FieldDef);

/** Anything but the literal string "false" keeps the entry on. */
function normalize(draft: Record<string, string>): Record<string, string> {
  const raw = draft[ENABLED];
  if (raw === undefined) return draft;
  const next = raw === "false" ? "false" : "true";
  return next === raw ? draft : { ...draft, [ENABLED]: next };
}

export function AdminAboutPage() {
  return (
    <AdminSection>
      <AboutContent />
    </AdminSection>
  );
}

function AboutContent() {
  const t = useT();
  const { branding } = useBranding();
  const policy = useFeaturePolicy();
  const [info, setInfo] = useState<VersionInfo | null>(null);
  const [cached, setCached] = useState<LatestRelease | null>(null);
  const [dialog, setDialog] = useState<{ open: boolean; busy: boolean; outcome: CheckOutcome | null }>({ open: false, busy: false, outcome: null });
  const inFlight = useRef(false);

  useEffect(() => {
    const controller = new AbortController();
    fetchVersion(controller.signal).then(setInfo, () => undefined);
    return () => controller.abort();
  }, []);

  useEffect(() => {
    const read = () => setCached(readCachedRelease());
    read();
    window.addEventListener(LATEST_RELEASE_EVENT, read);
    window.addEventListener("storage", read);
    return () => {
      window.removeEventListener(LATEST_RELEASE_EVENT, read);
      window.removeEventListener("storage", read);
    };
  }, []);

  const current = info?.version ?? "";
  const hasUpdate = useMemo(() => Boolean(cached && current && isUpdateAvailable(current, cached.version)), [cached, current]);

  const check = useCallback(async () => {
    if (inFlight.current) return;
    inFlight.current = true;
    setDialog({ open: true, busy: true, outcome: null });
    try {
      const outcome = await checkLatestRelease(current);
      if (outcome.state !== "failed") cacheRelease(outcome.latest);
      setDialog({ open: true, busy: false, outcome });
    } finally {
      inFlight.current = false;
    }
  }, [current]);

  const form = useSettingsForm({ fields: FIELDS, normalize });

  return (
    <AdminPage title={t("adminAbout.title")} description={t("adminAbout.description")}>
      <Section>
        <div className="flex flex-wrap items-start justify-between gap-4">
          <div className="space-y-2">
            <div className="flex flex-wrap items-center gap-2">
              <h3 className="text-lg font-semibold">{branding.title}</h3>
              <Badge tone="outline">{t("adminAbout.product.console")}</Badge>
              <Badge tone={hasUpdate ? "danger" : "neutral"} className="gap-1">
                {hasUpdate ? <ArrowUpCircle aria-hidden className="size-3" /> : null}
                {current ? withV(current) : t("adminAbout.product.unknown")}
                {hasUpdate ? <span className="sr-only">{t("adminAbout.update.badge")}</span> : null}
              </Badge>
            </div>
            <dl className="grid grid-cols-[auto_1fr] gap-x-4 gap-y-1 text-sm">
              <dt className="text-muted-foreground">{t("adminAbout.product.version")}</dt>
              <dd>
                <Mono>{current ? withV(current) : t("adminAbout.product.unknown")}</Mono>
              </dd>
              {info?.buildID ? (
                <>
                  <dt className="text-muted-foreground">{t("adminAbout.product.build")}</dt>
                  <dd>
                    <Mono>{info.buildID}</Mono>
                  </dd>
                </>
              ) : null}
              {info?.commit ? (
                <>
                  <dt className="text-muted-foreground">{t("adminAbout.product.commit")}</dt>
                  <dd>
                    <Mono>{info.commit.slice(0, 12)}</Mono>
                  </dd>
                </>
              ) : null}
            </dl>
            {hasUpdate && cached ? <p className="text-sm text-destructive">{t("adminAbout.update.latestKnown", { latest: withV(cached.version) })}</p> : null}
            <p className="flex items-start gap-2 text-[13px] text-muted-foreground">
              <ShieldCheck aria-hidden className="mt-0.5 size-4 shrink-0" />
              {t("adminAbout.product.copyright")}
            </p>
          </div>
          <Button variant="outline" onClick={check} loading={dialog.busy}>
            <RefreshCw />
            {t("adminAbout.update.check")}
          </Button>
        </div>
      </Section>

      <SettingsGroup
        form={form}
        fields={FIELDS}
        title={t("adminAbout.desktop.title")}
        description={t("adminAbout.desktop.description")}
        onSaved={() =>
          policy.patch({
            desktopDownloadEnabled: boolOf(form.draft, ENABLED, true),
            desktopDownloadURL: (form.draft[URL_ID] ?? "").trim() || DEFAULT_DOWNLOAD_URL,
          })
        }
      />

      <Dialog open={dialog.open} onOpenChange={(open) => !dialog.busy && setDialog((d) => ({ ...d, open }))}>
        <DialogContent>
          <UpdateDialogBody current={current} busy={dialog.busy} outcome={dialog.outcome} onRetry={check} onClose={() => setDialog((d) => ({ ...d, open: false }))} />
        </DialogContent>
      </Dialog>
    </AdminPage>
  );
}

function UpdateDialogBody({ current, busy, outcome, onRetry, onClose }: { current: string; busy: boolean; outcome: CheckOutcome | null; onRetry: () => void; onClose: () => void }) {
  const t = useT();
  if (busy || !outcome) {
    return (
      <DialogHeader>
        <DialogTitle>{t("adminAbout.update.checking")}</DialogTitle>
        <DialogDescription>{t("adminAbout.update.check")}</DialogDescription>
      </DialogHeader>
    );
  }
  if (outcome.state === "failed") {
    return (
      <>
        <DialogHeader>
          <DialogTitle>{t("adminAbout.update.failedTitle")}</DialogTitle>
          <DialogDescription>{t("adminAbout.update.failedBody")}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button variant="outline" onClick={onClose}>
            {t("common.actions.close")}
          </Button>
          <Button onClick={onRetry}>{t("adminAbout.update.retry")}</Button>
        </DialogFooter>
      </>
    );
  }
  if (outcome.state === "current") {
    return (
      <>
        <DialogHeader>
          <DialogTitle>{t("adminAbout.update.currentTitle")}</DialogTitle>
          <DialogDescription>{t("adminAbout.update.currentBody", { current: withV(current || outcome.latest.version) })}</DialogDescription>
        </DialogHeader>
        <DialogFooter>
          <Button onClick={onClose}>{t("common.actions.close")}</Button>
        </DialogFooter>
      </>
    );
  }
  return (
    <>
      <DialogHeader>
        <DialogTitle>{t("adminAbout.update.availableTitle")}</DialogTitle>
        <DialogDescription>{t("adminAbout.update.availableBody", { current: current ? withV(current) : t("adminAbout.product.unknown"), latest: withV(outcome.latest.version) })}</DialogDescription>
      </DialogHeader>
      <DialogFooter>
        <Button variant="outline" onClick={onClose}>
          {t("common.actions.close")}
        </Button>
        <Button asChild>
          <a href={outcome.latest.url} target="_blank" rel="noopener noreferrer">
            <ExternalLink />
            {t("adminAbout.update.openRelease")}
          </a>
        </Button>
      </DialogFooter>
    </>
  );
}
