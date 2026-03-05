import { resolveAccessToken } from "@/lib/api/client";
import { sessionStore } from "@/lib/api/session-store";
import { isDesktop } from "@/lib/api/platform";
import { shell } from "../desktop/bridge";

export type ExistingSession = "signed-in" | "none" | "local-failed";

/**
 * Is somebody already signed in? Browsers answer through the refresh cookie.
 * The desktop app in local mode has no login form at all: it obtains a session
 * from the bundled server through the shell.
 */
export async function findExistingSession(): Promise<{ result: ExistingSession; localMode: boolean }> {
  if (await resolveAccessToken()) return { result: "signed-in", localMode: false };
  if (!isDesktop()) return { result: "none", localMode: false };
  let localMode = false;
  try {
    const server = await shell.getServer();
    localMode = server?.mode === "local";
    if (localMode) {
      const credentials = await shell.localSignIn();
      if (credentials) {
        sessionStore.set(credentials);
        return { result: "signed-in", localMode };
      }
      return { result: "local-failed", localMode };
    }
  } catch {
    return { result: localMode ? "local-failed" : "none", localMode };
  }
  return { result: "none", localMode };
}
