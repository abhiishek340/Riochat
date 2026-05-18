/** Paging conventions of the admin API: `{ total, results }` envelopes, page size 25. */

export const DEFAULT_PAGE = 1;
export const DEFAULT_PAGE_SIZE = 25;
export const PAGE_SIZE_OPTIONS = [10, 25, 50, 100] as const;
export const MAX_PAGE_SIZE = 1000;

export interface PageResult<T> {
  total: number;
  results: T[];
}

export function normalizePage(page: number | undefined, pageSize: number | undefined): { page: number; pageSize: number } {
  const p = typeof page === "number" && Number.isFinite(page) && page > 0 ? Math.floor(page) : DEFAULT_PAGE;
  const s = typeof pageSize === "number" && Number.isFinite(pageSize) && pageSize > 0 ? Math.floor(pageSize) : DEFAULT_PAGE_SIZE;
  return { page: p, pageSize: s };
}

/** Tolerates missing fields: a missing total counts as 0 and missing results as an empty list. */
export function readPage<T>(raw: unknown): PageResult<T> {
  const value = (typeof raw === "object" && raw !== null ? raw : {}) as { total?: unknown; results?: unknown; items?: unknown };
  const list = Array.isArray(value.results) ? value.results : Array.isArray(value.items) ? value.items : [];
  return { total: typeof value.total === "number" && Number.isFinite(value.total) ? value.total : 0, results: list as T[] };
}

export function pageCount(total: number, pageSize: number): number {
  return Math.max(1, Math.ceil(Math.max(0, total) / Math.max(1, pageSize)));
}

/**
 * Fetches every page: requests of at most 1000 rows, stopping once `total`
 * rows were gathered or a page comes back empty.
 */
export async function loadAllPages<T>(
  fetchPage: (page: number, pageSize: number) => Promise<PageResult<T>>,
  pageSize: number = MAX_PAGE_SIZE,
): Promise<T[]> {
  const size = Math.min(MAX_PAGE_SIZE, Math.max(1, Math.floor(pageSize)));
  const out: T[] = [];
  for (let page = 1; ; page++) {
    const current = await fetchPage(page, size);
    if (current.results.length === 0) break;
    out.push(...current.results);
    if (out.length >= current.total) break;
  }
  return out;
}

/** Page to show after `removed` rows left a list: step back when the page became empty. */
export function pageAfterRemoval(page: number, rowsOnPage: number, removed: number): number {
  return page > 1 && rowsOnPage - removed <= 0 ? page - 1 : page;
}
