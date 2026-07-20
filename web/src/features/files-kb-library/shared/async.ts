import { NetworkError, isAbortError } from "@/lib/api/errors";

export function chunk<T>(items: readonly T[], size: number): T[][] {
  const out: T[][] = [];
  for (let i = 0; i < items.length; i += size) out.push(items.slice(i, i + size));
  return out;
}

export type Settled<R> = { ok: true; value: R } | { ok: false; error: unknown };

/**
 * Runs `worker` over `items` with at most `limit` in flight. Results keep the
 * input order. Once `signal` aborts no new item is started; those never
 * started are reported as aborted failures.
 */
export async function runPool<T, R>(
  items: readonly T[],
  limit: number,
  worker: (item: T, index: number) => Promise<R>,
  signal?: AbortSignal,
): Promise<Settled<R>[]> {
  const results: Settled<R>[] = new Array(items.length);
  let next = 0;
  const lane = async (): Promise<void> => {
    while (next < items.length) {
      const index = next;
      next += 1;
      if (signal?.aborted) {
        results[index] = { ok: false, error: new DOMException("Aborted", "AbortError") };
        continue;
      }
      try {
        results[index] = { ok: true, value: await worker(items[index] as T, index) };
      } catch (error) {
        results[index] = { ok: false, error };
      }
    }
  };
  await Promise.all(Array.from({ length: Math.max(1, Math.min(limit, items.length)) }, lane));
  return results;
}

/** Sequential chunks; `onProgress` receives the number of items processed so far. */
export async function runChunks<T>(
  items: readonly T[],
  size: number,
  worker: (item: T) => Promise<void>,
  onProgress?: (processed: number, total: number) => void,
): Promise<{ success: number; failed: number; firstError: unknown }> {
  let success = 0;
  let failed = 0;
  let firstError: unknown;
  let processed = 0;
  for (const group of chunk(items, size)) {
    for (const item of group) {
      try {
        await worker(item);
        success += 1;
      } catch (error) {
        failed += 1;
        if (firstError === undefined) firstError = error;
      }
      processed += 1;
    }
    onProgress?.(processed, items.length);
  }
  return { success, failed, firstError };
}

export function sleep(ms: number, signal?: AbortSignal): Promise<void> {
  return new Promise((resolve, reject) => {
    if (signal?.aborted) {
      reject(new DOMException("Aborted", "AbortError"));
      return;
    }
    const id = setTimeout(() => {
      signal?.removeEventListener("abort", onAbort);
      resolve();
    }, ms);
    const onAbort = () => {
      clearTimeout(id);
      reject(new DOMException("Aborted", "AbortError"));
    };
    signal?.addEventListener("abort", onAbort, { once: true });
  });
}

/**
 * Retries only when the request never produced an HTTP response
 * (NetworkError). Delays: 250 ms then 500 ms, each plus up to 150 ms jitter.
 */
export async function retryNetwork<R>(
  run: () => Promise<R>,
  options: { retries?: number; baseDelayMs?: number; jitterMs?: number; signal?: AbortSignal; random?: () => number; wait?: (ms: number) => Promise<void> } = {},
): Promise<R> {
  const retries = options.retries ?? 2;
  const base = options.baseDelayMs ?? 250;
  const jitter = options.jitterMs ?? 150;
  const random = options.random ?? Math.random;
  const wait = options.wait ?? ((ms: number) => sleep(ms, options.signal));
  for (let attempt = 0; ; attempt += 1) {
    try {
      return await run();
    } catch (error) {
      if (isAbortError(error) || !(error instanceof NetworkError) || attempt >= retries) throw error;
      await wait(base * 2 ** attempt + random() * jitter);
    }
  }
}

/** Order-preserving de-duplication of `items` by key, first one wins. */
export function uniqueBy<T>(items: readonly T[], key: (item: T) => string): T[] {
  const seen = new Set<string>();
  const out: T[] = [];
  for (const item of items) {
    const k = key(item);
    if (seen.has(k)) continue;
    seen.add(k);
    out.push(item);
  }
  return out;
}

/** Trimmed, de-duplicated, non-empty IDs. */
export function cleanIDs(ids: readonly string[]): string[] {
  return uniqueBy(
    ids.map((id) => id.trim()).filter((id) => id !== ""),
    (id) => id,
  );
}
