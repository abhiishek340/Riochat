/**
 * Update notices for the desktop app, kept free of React and of the real shell
 * so the rules can be tested: a version is announced at most once per page
 * lifetime, "auto" installs only after the user agrees, "notify" points at the
 * releases page, and "disabled" (or any lookup failure) says nothing.
 */
import type { Distribution } from "./distribution";

export interface UpdateOffer {
  kind: "install" | "download";
  version: string;
  /** Installer handle for "install"; the releases page for "download". */
  rid?: number;
  releasesUrl?: string;
}

export interface UpdateSource {
  /** Updater plugin: resolves null when up to date. */
  checkInstallable: () => Promise<{ rid: number; version: string } | null>;
  /** Portable builds: the shell reports a newer version without installing. */
  checkNotice: () => Promise<{ version: string } | null>;
}

export function createUpdateChecker(source: UpdateSource) {
  const announced = new Set<string>();
  return async function check(distribution: Distribution): Promise<UpdateOffer | null> {
    try {
      if (distribution.updateMode === "auto") {
        const found = await source.checkInstallable();
        if (!found || announced.has(found.version)) return null;
        announced.add(found.version);
        return { kind: "install", version: found.version, rid: found.rid };
      }
      if (distribution.updateMode === "notify") {
        const notice = await source.checkNotice();
        if (!notice || announced.has(notice.version)) return null;
        announced.add(notice.version);
        return { kind: "download", version: notice.version, releasesUrl: distribution.releasesUrl };
      }
    } catch {
      /* background checks never bother the user */
    }
    return null;
  };
}

export const UPDATE_CHECK_INTERVAL_MS = 4 * 60 * 60 * 1000;
export const UPDATE_TOAST_ID = "desktop-update";
