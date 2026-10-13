/** Builds the one-click provider import address. Nothing here talks to a server. */

export const IMPORT_PROTOCOLS = [
  "openai_chat_completions",
  "openai_responses",
  "anthropic_messages",
  "google_generate_content",
  "gemini_interactions",
  "xai_responses",
  "openrouter_chat_completions",
  "openrouter_responses",
] as const;

export interface ImportEntry {
  url: string;
  key: string;
  protocol?: string;
  name?: string;
}

function encodeEntry(entry: ImportEntry): string {
  const parts = [`url=${entry.url.trim() ? encodeURIComponent(entry.url.trim()) : "{address}"}`, `key=${entry.key.trim() ? encodeURIComponent(entry.key.trim()) : "{key}"}`];
  if (entry.protocol?.trim()) parts.push(`protocol=${encodeURIComponent(entry.protocol.trim())}`);
  if (entry.name?.trim()) parts.push(`name=${encodeURIComponent(entry.name.trim())}`);
  return parts.join("&");
}

/**
 * `<origin>/import#v=1&url=...&key=...` for one entry (older clients read
 * version 1), `v=2` followed by repeated groups for several.
 */
export function buildImportLink(origin: string, entries: readonly ImportEntry[]): string {
  const base = origin.replace(/\/+$/, "");
  const list = entries.length ? entries : [{ url: "", key: "" }];
  const version = list.length > 1 ? 2 : 1;
  return `${base}/import#v=${version}&${list.map(encodeEntry).join("&")}`;
}
