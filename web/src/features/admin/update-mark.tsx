"use client";

import { ArrowUpCircle } from "lucide-react";
import { useEffect, useMemo, useState } from "react";
import { Tooltip } from "@/components/ui/tooltip";
import { fetchVersion } from "@/lib/api/endpoints/public";
import { useT } from "@/lib/i18n/provider";
import { isUpdateAvailable, LATEST_RELEASE_EVENT, readCachedRelease, withV, type LatestRelease } from "./lib/update-check";

/**
 * Small "update available" arrow for the About entry of the sidebar. It reads
 * the release record the About page caches (nothing is requested from the
 * release service here) and the running version from the public endpoint.
 */
export function AdminUpdateMark() {
  const t = useT();
  const [latest, setLatest] = useState<LatestRelease | null>(null);
  const [current, setCurrent] = useState("");

  useEffect(() => {
    const read = () => setLatest(readCachedRelease());
    read();
    window.addEventListener(LATEST_RELEASE_EVENT, read);
    window.addEventListener("storage", read);
    return () => {
      window.removeEventListener(LATEST_RELEASE_EVENT, read);
      window.removeEventListener("storage", read);
    };
  }, []);

  // The version is only worth asking for once a release is known.
  const known = latest !== null;
  useEffect(() => {
    if (!known) return;
    const controller = new AbortController();
    fetchVersion(controller.signal).then(
      (info) => setCurrent(info.version ?? ""),
      () => undefined,
    );
    return () => controller.abort();
  }, [known]);

  const available = useMemo(() => Boolean(latest && current && isUpdateAvailable(current, latest.version)), [latest, current]);
  if (!available || !latest) return null;
  const hint = t("adminAbout.update.sidebarHint", { current: withV(current), latest: withV(latest.version) });
  return (
    <Tooltip label={hint} side="right">
      <span role="img" aria-label={t("adminAbout.update.badge")} className="ml-auto inline-flex shrink-0 text-primary">
        <ArrowUpCircle className="size-4" aria-hidden />
      </span>
    </Tooltip>
  );
}
