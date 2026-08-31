/**
 * Builds one-click import links. The link lives in the URL fragment so the
 * key never reaches a server; nothing here talks to the network.
 */
import { IMPORT_PROTOCOLS } from "./protocols";

export const MAX_LINK_ENTRIES = 50;

export interface LinkEntryDraft {
  /** Stable identity so removing an entry does not shuffle the others' inputs. */
  id: number;
  url: string;
  key: string;
  protocol: string;
  name: string;
}

export function emptyEntry(id: number): LinkEntryDraft {
  return { id, url: "", key: "", protocol: "", name: "" };
}

const enc = encodeURIComponent;

function entryPairs(entry: LinkEntryDraft): string[] {
  const pairs = [`url=${entry.url.trim() ? enc(entry.url.trim()) : "{address}"}`, `key=${entry.key.trim() ? enc(entry.key.trim()) : "{key}"}`];
  if (entry.protocol && (IMPORT_PROTOCOLS as readonly string[]).includes(entry.protocol)) pairs.push(`protocol=${enc(entry.protocol)}`);
  if (entry.name.trim()) pairs.push(`name=${enc(entry.name.trim())}`);
  return pairs;
}

/**
 * `<origin>/import#v=1&url=…&key=…` for one entry (always version 1 so older
 * clients open it), `#v=2&` plus repeated groups for several. Empty required
 * values stay readable as `{address}` / `{key}`. Empty until the origin is known.
 */
export function buildImportLink(origin: string, entries: readonly LinkEntryDraft[]): string {
  const base = origin.replace(/\/+$/, "");
  if (!base || entries.length === 0) return "";
  const version = entries.length === 1 ? 1 : 2;
  const parts = [`v=${version}`];
  for (const entry of entries) parts.push(...entryPairs(entry));
  return `${base}/import#${parts.join("&")}`;
}
