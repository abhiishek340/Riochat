"use client";

import { useCapabilities } from "@/lib/capabilities/provider";
import { DesktopNotifiers } from "../desktop/notifiers";
import { OnboardingOverlay } from "../onboarding/overlay";
import { AnnouncementsHost } from "./announcements-host";
import { SearchHost } from "./search-host";

/**
 * Everything the signed-in shell hosts besides the page: the first-run wizard,
 * the announcement dialog, global search with its shortcuts and the desktop
 * notifiers. Mount it once inside the session providers.
 */
export function ShellExtras() {
  const { flags } = useCapabilities();
  return (
    <>
      <OnboardingOverlay />
      {flags.announcements ? <AnnouncementsHost /> : null}
      <SearchHost />
      <DesktopNotifiers />
    </>
  );
}
