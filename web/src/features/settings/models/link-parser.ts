/**
 * Strict parser of import-link fragments. Any problem rejects the whole link
 * with a typed reason; the page turns the reason into text.
 */
import { isImportProtocol, type ImportProtocol } from "./protocols";

export const MAX_FRAGMENT_BYTES = 64 * 1024;
export const MAX_URL_LENGTH = 512;
export const MAX_KEY_LENGTH = 512;
export const MAX_NAME_LENGTH = 64;
export const MAX_ENTRIES = 50;

export type LinkErrorReason =
  | "empty"
  | "unsupportedVersion"
  | "unknownParam"
  | "repeatedParam"
  | "missingUrl"
  | "invalidUrl"
  | "insecureUrl"
  | "missingKey"
  | "invalidKey"
  | "invalidProtocol"
  | "invalidName"
  | "tooManyEntries"
  | "duplicateEntry";

export interface LinkError {
  reason: LinkErrorReason;
  /** 1-based entry number, only for multi-entry links. */
  entry?: number;
}

export interface LinkEntry {
  baseURL: string;
  host: string;
  key: string;
  protocol?: ImportProtocol;
  name?: string;
}

export type ParseResult = { ok: true; version: 1 | 2; entries: LinkEntry[] } | { ok: false; error: LinkError };

const ALLOWED = new Set(["v", "url", "key", "protocol", "name"]);

function fail(reason: LinkErrorReason, entry?: number): ParseResult {
  return { ok: false, error: entry === undefined ? { reason } : { reason, entry } };
}

function decode(value: string): string | null {
  try {
    return decodeURIComponent(value.replace(/\+/g, " "));
  } catch {
    return null;
  }
}

interface Pair {
  name: string;
  value: string;
}

/** Normalises an https API base URL: no credentials, query or fragment; lower-case host. */
export function normalizeBaseUrl(raw: string): { url: string; host: string } | "insecure" | null {
  const value = raw.trim();
  if (!value || value.length > MAX_URL_LENGTH) return null;
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    return null;
  }
  if (parsed.protocol === "http:") return "insecure";
  if (parsed.protocol !== "https:") return null;
  if (parsed.username || parsed.password || parsed.search || parsed.hash || value.includes("?") || value.includes("#")) return null;
  if (!parsed.hostname) return null;
  const path = parsed.pathname.replace(/\/+$/, "");
  return { url: `${parsed.protocol}//${parsed.host}${path}`, host: parsed.hostname.toLowerCase() };
}

function validKey(key: string): boolean {
  if (key.length < 1 || key.length > MAX_KEY_LENGTH) return false;
  for (let i = 0; i < key.length; i++) {
    const c = key.charCodeAt(i);
    if (c < 0x21 || c > 0x7e) return false;
  }
  return true;
}

function hasControlChars(text: string): boolean {
  for (let i = 0; i < text.length; i++) {
    const c = text.charCodeAt(i);
    if (c < 0x20 || c === 0x7f) return true;
  }
  return false;
}

function buildEntry(fields: Map<string, string>, number: number | undefined): { entry: LinkEntry } | { error: LinkError } {
  const err = (reason: LinkErrorReason): { error: LinkError } => ({ error: number === undefined ? { reason } : { reason, entry: number } });
  const rawUrl = (fields.get("url") ?? "").trim();
  if (!rawUrl) return err("missingUrl");
  const normalized = normalizeBaseUrl(rawUrl);
  if (normalized === "insecure") return err("insecureUrl");
  if (normalized === null) return err("invalidUrl");

  const rawKey = (fields.get("key") ?? "").trim();
  if (!rawKey) return err("missingKey");
  if (!validKey(rawKey)) return err("invalidKey");

  const entry: LinkEntry = { baseURL: normalized.url, host: normalized.host, key: rawKey };
  const protocol = fields.get("protocol");
  if (protocol !== undefined) {
    if (!isImportProtocol(protocol)) return err("invalidProtocol");
    entry.protocol = protocol;
  }
  const name = fields.get("name");
  if (name !== undefined) {
    const trimmed = name.trim();
    if (trimmed.length > MAX_NAME_LENGTH || hasControlChars(trimmed)) return err("invalidName");
    if (trimmed) entry.name = trimmed;
  }
  return { entry };
}

export function parseImportFragment(fragment: string): ParseResult {
  const text = fragment.startsWith("#") ? fragment.slice(1) : fragment;
  if (!text.trim()) return fail("empty");
  if (new TextEncoder().encode(text).length > MAX_FRAGMENT_BYTES) return fail("invalidUrl");

  const pairs: Pair[] = [];
  for (const chunk of text.split("&")) {
    if (chunk === "") continue;
    const eq = chunk.indexOf("=");
    const rawName = eq < 0 ? chunk : chunk.slice(0, eq);
    const rawValue = eq < 0 ? "" : chunk.slice(eq + 1);
    const name = decode(rawName);
    const value = decode(rawValue);
    if (name === null || value === null) return fail("invalidUrl");
    if (!ALLOWED.has(name)) return fail("unknownParam");
    pairs.push({ name, value });
  }
  if (pairs.length === 0) return fail("empty");

  const versions = pairs.filter((p) => p.name === "v");
  if (versions.length > 1) return fail("repeatedParam");
  const versionText = versions[0]?.value.trim() ?? "1";
  if (versionText !== "1" && versionText !== "2") return fail("unsupportedVersion");
  const rest = pairs.filter((p) => p.name !== "v");

  if (versionText === "1") {
    const fields = new Map<string, string>();
    for (const p of rest) {
      if (fields.has(p.name)) return fail("repeatedParam");
      fields.set(p.name, p.value);
    }
    const built = buildEntry(fields, undefined);
    return "error" in built ? { ok: false, error: built.error } : { ok: true, version: 1, entries: [built.entry] };
  }

  const groups: Map<string, string>[] = [];
  for (const p of rest) {
    if (p.name === "url") {
      groups.push(new Map([["url", p.value]]));
      continue;
    }
    const current = groups[groups.length - 1];
    if (!current) return fail("missingUrl");
    if (current.has(p.name)) return fail("repeatedParam", groups.length);
    current.set(p.name, p.value);
  }
  if (groups.length === 0) return fail("missingUrl");
  if (groups.length > MAX_ENTRIES) return fail("tooManyEntries");

  const entries: LinkEntry[] = [];
  const seen = new Set<string>();
  for (let i = 0; i < groups.length; i++) {
    const built = buildEntry(groups[i] as Map<string, string>, i + 1);
    if ("error" in built) return { ok: false, error: built.error };
    const id = `${built.entry.baseURL}\u0000${built.entry.key}`;
    if (seen.has(id)) return fail("duplicateEntry", i + 1);
    seen.add(id);
    entries.push(built.entry);
  }
  return { ok: true, version: 2, entries };
}

/** First 3 and last 4 characters of a long key; shorter keys reveal less; bullets for the rest. */
export function maskKey(raw: string): string {
  const key = raw.trim();
  const n = key.length;
  let head = "";
  let tail = "";
  if (n >= 12) {
    head = key.slice(0, 3);
    tail = key.slice(-4);
  } else if (n >= 6) {
    tail = key.slice(-2);
  }
  const hidden = n - head.length - tail.length;
  const bullets = "•".repeat(Math.min(8, Math.max(4, hidden)));
  return `${head}${bullets}${tail}`;
}
