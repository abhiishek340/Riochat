/**
 * In-memory credential snapshot. The access token is never written to browser
 * storage; the long-lived refresh token is an HttpOnly cookie that page code
 * cannot read.
 *
 * Every change bumps a monotonic `revision`, notifies subscribers, raises the
 * window event `riochat:session-snapshot-changed` and (unless suppressed)
 * tells sibling tabs over a BroadcastChannel so they converge on the newest
 * session.
 */

export interface SessionSnapshot {
  accessToken: string;
  sessionID: string;
}

export const EMPTY_SESSION: SessionSnapshot = Object.freeze({ accessToken: "", sessionID: "" });

export const SESSION_CHANGED_EVENT = "riochat:session-snapshot-changed";
export const SESSION_CLEARED_EVENT = "riochat:session-cleared";
export const SESSION_CHANNEL = "riochat:session-snapshot";

export interface ChannelLike {
  postMessage(message: unknown): void;
  addEventListener(type: "message", listener: (event: { data: unknown }) => void): void;
  close(): void;
}

export interface SessionStoreOptions {
  /** Returns a cross-tab channel, or null when tabs must not share sessions. */
  channel?: (() => ChannelLike | null) | null;
  /** Receives window-level events; null disables them (tests, SSR). */
  events?: Pick<EventTarget, "dispatchEvent"> | null;
}

export interface WriteOptions {
  /** Skip the cross-tab broadcast (used for failed-refresh clears). */
  silent?: boolean;
}

function isSnapshot(value: unknown): value is SessionSnapshot {
  if (typeof value !== "object" || value === null) return false;
  const v = value as Record<string, unknown>;
  return typeof v.accessToken === "string" && typeof v.sessionID === "string";
}

export class SessionStore {
  private snapshot: SessionSnapshot = EMPTY_SESSION;
  private rev = 0;
  private readonly listeners = new Set<(s: SessionSnapshot) => void>();
  private channel: ChannelLike | null = null;
  private readonly events: Pick<EventTarget, "dispatchEvent"> | null;

  constructor(options: SessionStoreOptions = {}) {
    this.events = options.events === undefined ? defaultEvents() : options.events;
    try {
      this.channel = options.channel ? options.channel() : null;
      this.channel?.addEventListener("message", (event) => this.onMessage(event.data));
    } catch {
      this.channel = null;
    }
  }

  get(): SessionSnapshot {
    return this.snapshot;
  }

  token(): string {
    return this.snapshot.accessToken;
  }

  revision(): number {
    return this.rev;
  }

  subscribe(listener: (s: SessionSnapshot) => void): () => void {
    this.listeners.add(listener);
    return () => this.listeners.delete(listener);
  }

  /** Stores credentials. Writing identical values changes nothing. */
  set(next: SessionSnapshot, options: WriteOptions = {}): void {
    this.apply(next, !options.silent);
  }

  clear(options: WriteOptions = {}): void {
    this.apply(EMPTY_SESSION, !options.silent);
  }

  private apply(next: SessionSnapshot, broadcast: boolean): void {
    if (next.accessToken === this.snapshot.accessToken && next.sessionID === this.snapshot.sessionID) return;
    this.snapshot = next.accessToken || next.sessionID ? { accessToken: next.accessToken, sessionID: next.sessionID } : EMPTY_SESSION;
    this.rev += 1;
    for (const l of [...this.listeners]) l(this.snapshot);
    try {
      this.events?.dispatchEvent(new CustomEvent(SESSION_CHANGED_EVENT, { detail: this.snapshot }));
    } catch {
      /* no DOM */
    }
    if (broadcast) {
      try {
        this.channel?.postMessage({ type: "session_snapshot", snapshot: this.snapshot });
      } catch {
        /* channel closed */
      }
    }
  }

  private onMessage(data: unknown): void {
    if (typeof data !== "object" || data === null) return;
    const msg = data as { type?: unknown; snapshot?: unknown };
    if (msg.type !== "session_snapshot" || !isSnapshot(msg.snapshot)) return;
    this.apply({ accessToken: msg.snapshot.accessToken, sessionID: msg.snapshot.sessionID }, false);
  }
}

function defaultEvents(): EventTarget | null {
  return typeof window === "undefined" ? null : window;
}

function browserChannel(): ChannelLike | null {
  if (typeof window === "undefined" || typeof BroadcastChannel === "undefined") return null;
  // Desktop tabs may be bound to different servers on one origin; sharing
  // tokens between them would hand one server another server's credential.
  if (window.__RIOCHAT_DESKTOP__ === true) return null;
  return new BroadcastChannel(SESSION_CHANNEL);
}

export const sessionStore = new SessionStore({ channel: browserChannel });

type CleanupHook = () => Promise<void> | void;
let cleanup: CleanupHook | null = null;

/** The desktop shell registers a hook that drops its keychain credential. */
export function setSignOutCleanup(hook: CleanupHook | null): void {
  cleanup = hook;
}

/**
 * Local sign-out: drop credentials (broadcast to other tabs), announce it,
 * run the optional cleanup, then leave for the login page.
 */
export async function endSession(): Promise<void> {
  sessionStore.clear();
  try {
    window.dispatchEvent(new CustomEvent(SESSION_CLEARED_EVENT));
  } catch {
    /* no DOM */
  }
  try {
    await cleanup?.();
  } catch {
    /* cleanup failures must not trap the user in a signed-out shell */
  }
  if (typeof window !== "undefined") window.location.replace("/login");
}
