/**
 * Runtime platform detection. "Desktop" means the page runs inside the native
 * shell, which sets a boolean flag on `window` at document start. This is a
 * UX convenience, not a security boundary: the backend decides how refresh
 * tokens are delivered from `X-Client-Platform` plus the request origin.
 */

declare global {
  interface Window {
    __RIOCHAT_DESKTOP__?: boolean;
  }
}

export function isDesktop(): boolean {
  return typeof window !== "undefined" && window.__RIOCHAT_DESKTOP__ === true;
}

export const CLIENT_PLATFORM_HEADER = "X-Client-Platform";

/** The origin to put into links meant for other people. */
export function publicWebOrigin(apiBase: string): string {
  if (typeof window === "undefined") return "";
  return isDesktop() ? apiBase : window.location.origin;
}
