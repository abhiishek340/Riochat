/**
 * Thin typed wrapper over the desktop shell's IPC. Everything here is a no-op
 * (or rejects with `unavailable`) in a plain browser, so callers can guard with
 * `isDesktop()` and otherwise ignore the module.
 *
 * Command names are the contract with the shell and must not change.
 */
import { isDesktop } from "@/lib/api/platform";
import { isRecord } from "@/lib/utils/guards";

type Json = Record<string, unknown>;

interface TauriInternals {
  invoke: (cmd: string, args?: Json) => Promise<unknown>;
  transformCallback?: (cb: (payload: unknown) => void, once?: boolean) => number;
}

function internals(): TauriInternals | null {
  if (typeof window === "undefined") return null;
  const w = window as unknown as { __TAURI_INTERNALS__?: TauriInternals };
  return w.__TAURI_INTERNALS__ && typeof w.__TAURI_INTERNALS__.invoke === "function" ? w.__TAURI_INTERNALS__ : null;
}

export const SHELL_ERROR_KINDS = ["network", "http", "storage", "no_session", "invalid_origin", "sidecar", "no_server", "tabs", "policy", "unavailable", "other"] as const;
export type ShellErrorKind = (typeof SHELL_ERROR_KINDS)[number];

/** Shell commands reject with a string, or with `{kind, message, status?, errorCode?}`. */
export class ShellError extends Error {
  readonly kind: ShellErrorKind;
  readonly status?: number;
  readonly errorCode?: string;
  constructor(kind: ShellErrorKind, message: string, extra: { status?: number; errorCode?: string } = {}) {
    super(message);
    this.name = "ShellError";
    this.kind = kind;
    this.status = extra.status;
    this.errorCode = extra.errorCode;
  }
}

export function toShellError(raw: unknown): ShellError {
  if (raw instanceof ShellError) return raw;
  if (typeof raw === "string") {
    // Policy rejections are plain strings prefixed with "policy:".
    return new ShellError(raw.startsWith("policy:") ? "policy" : "other", raw);
  }
  if (isRecord(raw)) {
    const kind = typeof raw.kind === "string" && (SHELL_ERROR_KINDS as readonly string[]).includes(raw.kind) ? (raw.kind as ShellErrorKind) : "other";
    const message = typeof raw.message === "string" ? raw.message : "";
    return new ShellError(kind, message, {
      status: typeof raw.status === "number" ? raw.status : undefined,
      errorCode: typeof raw.errorCode === "string" ? raw.errorCode : undefined,
    });
  }
  if (raw instanceof Error) return new ShellError("other", raw.message);
  return new ShellError("other", "");
}

export async function invoke<T = unknown>(command: string, args?: Json): Promise<T> {
  const bridge = isDesktop() ? internals() : null;
  if (!bridge) throw new ShellError("unavailable", "desktop shell is not available");
  try {
    return (await bridge.invoke(command, args)) as T;
  } catch (error) {
    throw toShellError(error);
  }
}

/** Subscribes to an app-wide shell event. Resolves to an unsubscribe function. */
export async function listen(event: string, handler: (payload: unknown) => void): Promise<() => void> {
  const bridge = isDesktop() ? internals() : null;
  if (!bridge || !bridge.transformCallback) return () => undefined;
  const callback = bridge.transformCallback((message) => {
    // The event plugin wraps the payload as {event, id, payload}.
    handler(isRecord(message) && "payload" in message ? message.payload : message);
  });
  const eventId = await bridge.invoke("plugin:event|listen", { event, target: { kind: "Any" }, handler: callback });
  return () => {
    void bridge.invoke("plugin:event|unlisten", { event, eventId }).catch(() => undefined);
  };
}

/** A channel argument for plugin commands that stream progress; the payloads are ignored. */
export function ignoredChannel(): string | null {
  const bridge = internals();
  if (!bridge?.transformCallback) return null;
  return `__CHANNEL__:${bridge.transformCallback(() => undefined)}`;
}

/** Opens an http(s) URL in the system browser. Returns false for anything else. */
export async function openExternal(url: string): Promise<boolean> {
  if (!/^https?:\/\//i.test(url)) return false;
  if (isDesktop()) {
    try {
      await invoke("plugin:opener|open_url", { url });
      return true;
    } catch {
      return false;
    }
  }
  const win = window.open(url, "_blank", "noopener,noreferrer");
  return win !== null;
}

export interface ShellServer {
  mode: "local" | "remote";
  origin: string;
}

export function readShellServer(raw: unknown): ShellServer | null {
  if (!isRecord(raw)) return null;
  const mode = raw.mode === "local" || raw.mode === "remote" ? raw.mode : null;
  const origin = typeof raw.origin === "string" ? raw.origin : "";
  return mode && origin ? { mode, origin } : null;
}

export interface ShellCredentials {
  accessToken: string;
  sessionID: string;
}

export function readCredentials(raw: unknown): ShellCredentials | null {
  if (!isRecord(raw)) return null;
  const accessToken = typeof raw.accessToken === "string" ? raw.accessToken : "";
  if (!accessToken) return null;
  return { accessToken, sessionID: typeof raw.sessionID === "string" ? raw.sessionID : "" };
}

export const shell = {
  getServer: async () => readShellServer(await invoke("get_server")),
  setRemoteServer: async (origin: string) => readShellServer(await invoke("set_remote_server", { origin })),
  setLocalServer: async () => readShellServer(await invoke("set_local_server")),
  leaveServer: () => invoke("leave_server"),
  storeSession: (refreshToken: string) => invoke("store_session", { refreshToken }),
  clearSession: () => invoke("clear_session"),
  /** Null means "no stored session"; other failures (a 401 from the server, say) are thrown. */
  refreshSession: async (): Promise<ShellCredentials | null> => {
    try {
      return readCredentials(await invoke("refresh_session"));
    } catch (error) {
      if (error instanceof ShellError && error.kind === "no_session") return null;
      throw error;
    }
  },
  localSignIn: async () => readCredentials(await invoke("local_sign_in")),
  startOauthLoopback: async () => String(await invoke("start_oauth_loopback")),
  stopOauthLoopback: () => invoke("stop_oauth_loopback"),
};
