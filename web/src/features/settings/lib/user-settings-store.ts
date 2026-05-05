/**
 * Client copy of the user's key/value settings.
 *
 * Writes apply to the local snapshot first and tell subscribers; when the
 * server refuses, the touched keys go back to what they were, unless a newer
 * local edit to the same key has happened in the meantime. A load that lands
 * while a local edit is still in flight never overwrites that key.
 */
import type { SettingsMap } from "./chat-settings";

export interface SettingsTransport {
  fetch: () => Promise<Record<string, string>>;
  save: (patch: Record<string, string>) => Promise<Record<string, string> | void>;
}

export interface SettingsSnapshot {
  values: SettingsMap;
  loaded: boolean;
  /** The latest load failed and nothing has been loaded since. */
  failed: boolean;
}

type Listener = () => void;

export class UserSettingsStore {
  private values: Record<string, string> = {};
  private loaded = false;
  private failed = false;
  private snapshot: SettingsSnapshot = { values: {}, loaded: false, failed: false };
  /** Bumped on every local edit of a key; identifies "the latest edit". */
  private revisions = new Map<string, number>();
  /** Keys with a save in flight (counted, since saves may overlap). */
  private inFlight = new Map<string, number>();
  private listeners = new Set<Listener>();
  private counter = 0;

  constructor(private readonly transport: SettingsTransport) {}

  subscribe = (listener: Listener): (() => void) => {
    this.listeners.add(listener);
    return () => {
      this.listeners.delete(listener);
    };
  };

  getSnapshot = (): SettingsSnapshot => this.snapshot;

  private publish(): void {
    this.snapshot = { values: { ...this.values }, loaded: this.loaded, failed: this.failed };
    for (const l of [...this.listeners]) l();
  }

  /** Loads from the server. Keys being edited locally keep their local value. */
  async load(): Promise<void> {
    let remote: Record<string, string>;
    try {
      remote = await this.transport.fetch();
    } catch (error) {
      // Keep whatever is already shown, but let subscribers offer a retry.
      if (!this.failed) {
        this.failed = true;
        this.publish();
      }
      throw error;
    }
    const next: Record<string, string> = { ...remote };
    for (const [key, count] of this.inFlight) {
      if (count > 0 && key in this.values) next[key] = this.values[key] as string;
    }
    this.values = next;
    this.loaded = true;
    this.failed = false;
    this.publish();
  }

  /** Forgets everything (sign-out or account switch). */
  reset(): void {
    this.values = {};
    this.loaded = false;
    this.failed = false;
    this.revisions.clear();
    this.inFlight.clear();
    this.publish();
  }

  /** Optimistic partial update. Rejects with the transport error after rolling back. */
  async update(patch: Record<string, string>): Promise<void> {
    const keys = Object.keys(patch);
    if (keys.length === 0) return;
    const before = new Map<string, string | undefined>();
    const mine = new Map<string, number>();
    for (const key of keys) {
      before.set(key, this.values[key]);
      const rev = ++this.counter;
      mine.set(key, rev);
      this.revisions.set(key, rev);
      this.inFlight.set(key, (this.inFlight.get(key) ?? 0) + 1);
      this.values[key] = patch[key] as string;
    }
    this.publish();
    try {
      await this.transport.save(patch);
    } catch (error) {
      for (const key of keys) {
        if (this.revisions.get(key) !== mine.get(key)) continue;
        const prior = before.get(key);
        if (prior === undefined) delete this.values[key];
        else this.values[key] = prior;
      }
      this.settle(keys);
      this.publish();
      throw error;
    }
    this.settle(keys);
  }

  private settle(keys: string[]): void {
    for (const key of keys) {
      const n = (this.inFlight.get(key) ?? 1) - 1;
      if (n <= 0) this.inFlight.delete(key);
      else this.inFlight.set(key, n);
    }
  }
}
