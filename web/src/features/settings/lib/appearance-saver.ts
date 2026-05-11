import {
  overlayAppearance,
  serializeAppearanceBundle,
  type AppearanceBundle,
  type FullAppearance,
} from "./appearance-prefs";

export interface AppearanceSaverDeps {
  /** Current device values; read when the timer fires. */
  readDevice: () => FullAppearance;
  /** Sends the serialised bundle to the account. */
  send: (serialized: string) => Promise<unknown>;
  /** False when nobody is signed in: nothing is sent. */
  canSend: () => boolean;
  onError: (error: unknown) => void;
  delayMs?: number;
}

/**
 * Collects appearance changes and writes them to the account once, a short
 * while after the last one. Several quick changes become one request carrying
 * the current device values overlaid with everything that was pending.
 */
export class AppearanceSaver {
  private pending: AppearanceBundle = {};
  private timer: ReturnType<typeof setTimeout> | null = null;
  private readonly delay: number;

  constructor(private readonly deps: AppearanceSaverDeps) {
    this.delay = deps.delayMs ?? 300;
  }

  schedule(patch: AppearanceBundle): void {
    this.pending = { ...this.pending, ...patch };
    if (this.timer) clearTimeout(this.timer);
    this.timer = setTimeout(() => void this.flush(), this.delay);
  }

  hasPending(): boolean {
    return Object.keys(this.pending).length > 0;
  }

  /** Sends right away whatever is pending. */
  async flush(): Promise<void> {
    if (this.timer) clearTimeout(this.timer);
    this.timer = null;
    const patch = this.pending;
    this.pending = {};
    if (Object.keys(patch).length === 0 || !this.deps.canSend()) return;
    try {
      await this.deps.send(serializeAppearanceBundle(overlayAppearance(this.deps.readDevice(), patch)));
    } catch (error) {
      this.deps.onError(error);
    }
  }
}
