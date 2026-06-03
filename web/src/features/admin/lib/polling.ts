/**
 * A bounded poller. Ticks run one after another; a hidden page keeps the
 * timer but skips the call; after `maxFailures` failures in a row it stops;
 * `start` and `stop` discard whatever answer is still in flight.
 */
export interface PollerOptions {
  intervalMs: number;
  maxFailures: number;
  /** Returns whether polling should continue. Throwing counts as a failure. */
  run: () => Promise<boolean>;
  isHidden?: () => boolean;
  setTimer?: (fn: () => void, ms: number) => unknown;
  clearTimer?: (handle: unknown) => void;
}

export interface Poller {
  start: () => void;
  stop: () => void;
  running: () => boolean;
}

export function createPoller(options: PollerOptions): Poller {
  const setTimer = options.setTimer ?? ((fn, ms) => setTimeout(fn, ms));
  const clearTimer = options.clearTimer ?? ((h) => clearTimeout(h as ReturnType<typeof setTimeout>));
  let handle: unknown = null;
  let active = false;
  let failures = 0;
  let generation = 0;

  const schedule = () => {
    if (!active) return;
    handle = setTimer(tick, options.intervalMs);
  };

  const tick = async () => {
    handle = null;
    if (!active) return;
    if (options.isHidden?.()) {
      schedule();
      return;
    }
    const mine = generation;
    try {
      const keepGoing = await options.run();
      if (mine !== generation || !active) return;
      failures = 0;
      if (keepGoing) schedule();
      else active = false;
    } catch {
      if (mine !== generation || !active) return;
      failures += 1;
      if (failures >= options.maxFailures) active = false;
      else schedule();
    }
  };

  return {
    start() {
      if (handle !== null) clearTimer(handle);
      handle = null;
      active = true;
      failures = 0;
      generation += 1;
      schedule();
    },
    stop() {
      active = false;
      generation += 1;
      if (handle !== null) clearTimer(handle);
      handle = null;
    },
    running: () => active,
  };
}
