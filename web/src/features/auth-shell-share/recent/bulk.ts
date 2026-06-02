/** Sequential, chunked execution for bulk actions, with per-item results. */

export interface ItemResult<T> {
  item: T;
  ok: boolean;
  error?: unknown;
}

export async function processInChunks<T>(
  items: readonly T[],
  size: number,
  handler: (item: T) => Promise<void>,
  options: { parallelWithinChunk?: boolean; onProgress?: (done: number, total: number) => void } = {},
): Promise<ItemResult<T>[]> {
  const results: ItemResult<T>[] = [];
  const run = async (item: T): Promise<ItemResult<T>> => {
    try {
      await handler(item);
      return { item, ok: true };
    } catch (error) {
      return { item, ok: false, error };
    }
  };
  for (let start = 0; start < items.length; start += size) {
    const chunk = items.slice(start, start + size);
    if (options.parallelWithinChunk) results.push(...(await Promise.all(chunk.map(run))));
    else for (const item of chunk) results.push(await run(item));
    options.onProgress?.(results.length, items.length);
  }
  return results;
}

export const ARCHIVE_CHUNK = 10;
export const DELETE_CHUNK = 10;
export const REVOKE_CHUNK = 100;
