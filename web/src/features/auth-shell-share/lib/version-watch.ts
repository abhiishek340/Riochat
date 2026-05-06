/**
 * Decides when a running tab is out of date. The first build identity seen is
 * the baseline; a later, different non-empty identity means the server was
 * upgraded underneath the tab. The first check always runs, later ones are
 * throttled and never overlap. Failures are silent.
 */
export const VERSION_CHECK_INTERVAL_MS = 10 * 60 * 1000;

export interface VersionWatcherOptions {
  /** Resolves the current build identity ("" when the server does not say). */
  fetchId: () => Promise<string>;
  onChanged: (id: string) => void;
  now?: () => number;
  minIntervalMs?: number;
}

export function createVersionWatcher(options: VersionWatcherOptions): (initial?: boolean) => Promise<void> {
  const now = options.now ?? Date.now;
  const minInterval = options.minIntervalMs ?? VERSION_CHECK_INTERVAL_MS;
  let baseline: string | null = null;
  let lastStarted = Number.NEGATIVE_INFINITY;
  let running = false;
  return async function check(initial = false): Promise<void> {
    if (running) return;
    if (!initial && now() - lastStarted < minInterval) return;
    running = true;
    lastStarted = now();
    try {
      const id = (await options.fetchId()).trim();
      if (!id) return;
      if (baseline === null) baseline = id;
      else if (id !== baseline) options.onChanged(id);
    } catch {
      /* offline or an old server: try again later */
    } finally {
      running = false;
    }
  };
}
