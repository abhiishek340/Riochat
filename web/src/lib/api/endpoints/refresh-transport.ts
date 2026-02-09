import { request } from "../http";
import type { SessionSnapshot } from "../session-store";
import { isRecord, str } from "../../utils/guards";

type NativeRefresh = () => Promise<SessionSnapshot | null>;
let nativeRefresh: NativeRefresh | null = null;

/**
 * The desktop shell holds the refresh token (OS credential store) and performs
 * the refresh itself; it registers that here so the shared client uses it
 * instead of the cookie-based call below.
 */
export function registerNativeRefresh(fn: NativeRefresh | null): void {
  nativeRefresh = fn;
}

/**
 * Browsers POST the refresh cookie (the server rotates it). The response
 * carries the new access token and session id; a missing token means "no
 * session".
 */
export async function refreshTransport(): Promise<SessionSnapshot | null> {
  if (nativeRefresh) return nativeRefresh();
  const data = await request<unknown>("/auth/refresh", { method: "POST" });
  if (!isRecord(data)) return null;
  const accessToken = str(data.accessToken);
  if (!accessToken) return null;
  return { accessToken, sessionID: str(data.sessionID) };
}
