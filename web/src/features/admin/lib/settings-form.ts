/** Pure half of the settings editor: field definitions, drafts, dirty tracking, patches. */
import type { MessageKey } from "@/lib/i18n/provider";

export type FieldType = "int" | "bool" | "string" | "password" | "textarea" | "json" | "select" | "tabs" | "multi";

export interface FieldOption {
  value: string;
  /** Catalog key; use `text` instead for names that are never translated. */
  labelKey?: MessageKey;
  text?: string;
}

export type Draft = Record<string, string>;

export interface FieldDef {
  ns: string;
  key: string;
  type: FieldType;
  label: MessageKey;
  hint?: MessageKey;
  /** Used when the server has no value, or (with `blankDefault`) a blank one. */
  def?: string;
  blankDefault?: boolean;
  options?: FieldOption[];
  /** `mb` fields are stored in bytes and shown in megabytes. */
  unit?: "mb";
  suffix?: string;
  placeholder?: string;
  /** Skip the trim that normally happens on save. */
  keepWhitespace?: boolean;
  visible?: (draft: Draft) => boolean;
  /** Secret: the server never returns the value, only `configured`. */
  secret?: boolean;
  /** Submit as `clear` when the draft is emptied on purpose is not supported; blank means keep. */
}

export interface SettingItem {
  key: string;
  value: string;
  valueType?: string;
  description?: string;
  sensitive?: boolean;
  configured?: boolean;
}

export type SettingsTree = Record<string, SettingItem[]>;

export interface PatchItem {
  namespace: string;
  key: string;
  value: string;
}

export function fid(ns: string, key: string): string {
  return `${ns}.${key}`;
}

export function fieldId(field: Pick<FieldDef, "ns" | "key">): string {
  return fid(field.ns, field.key);
}

export function findItem(tree: SettingsTree | null | undefined, ns: string, key: string): SettingItem | undefined {
  return tree?.[ns]?.find((item) => item.key === key);
}

/** Stored bytes shown as megabytes: integers plain, otherwise up to 6 decimals without trailing zeros. */
export function bytesToMb(value: string): string {
  const text = value.trim();
  if (text === "") return "";
  const bytes = Number(text);
  if (!Number.isFinite(bytes)) return text;
  const mb = bytes / 1048576;
  if (Number.isInteger(mb)) return String(mb);
  return String(Number(mb.toFixed(6)));
}

/** Megabytes typed by the admin back to a byte count; blank stays blank, junk is undefined. */
export function mbToBytes(value: string): string | undefined {
  const text = value.trim();
  if (text === "") return "";
  const mb = Number(text);
  if (!Number.isFinite(mb) || mb < 0) return undefined;
  return String(Math.round(mb * 1048576));
}

export function toDisplay(field: FieldDef, raw: string): string {
  return field.unit === "mb" ? bytesToMb(raw) : raw;
}

export interface BuiltDraft {
  saved: Draft;
  configured: Set<string>;
}

/** Reads every field out of a settings tree, applying defaults and unit conversion. */
export function buildDraft(fields: readonly FieldDef[], tree: SettingsTree): BuiltDraft {
  const saved: Draft = {};
  const configured = new Set<string>();
  for (const field of fields) {
    const id = fieldId(field);
    const item = findItem(tree, field.ns, field.key);
    if (item?.configured) configured.add(id);
    if (field.secret) {
      saved[id] = "";
      continue;
    }
    let raw = item?.value;
    if (raw === undefined || (field.blankDefault && raw.trim() === "")) raw = field.def ?? raw ?? "";
    saved[id] = toDisplay(field, raw);
  }
  return { saved, configured };
}

/** A value counts as present when the draft is non-blank or the server already holds one. */
export function hasValue(draft: Draft, configured: ReadonlySet<string>, id: string): boolean {
  return (draft[id] ?? "").trim() !== "" || configured.has(id);
}

export function isVisible(field: FieldDef, draft: Draft): boolean {
  return field.visible ? field.visible(draft) : true;
}

/** Only visible fields count: a hidden field is neither dirty nor saved. */
export function dirtyFields(fields: readonly FieldDef[], draft: Draft, saved: Draft): FieldDef[] {
  return fields.filter((field) => {
    if (!isVisible(field, draft)) return false;
    const id = fieldId(field);
    const current = draft[id] ?? "";
    return field.secret ? current.trim() !== "" : current !== (saved[id] ?? "");
  });
}

export function isDirty(fields: readonly FieldDef[], draft: Draft, saved: Draft): boolean {
  return dirtyFields(fields, draft, saved).length > 0;
}

/** Wire value of a draft: unit conversion and trimming. Undefined means "not a valid number". */
export function toWire(field: FieldDef, value: string): string | undefined {
  if (field.unit === "mb") return mbToBytes(value);
  return field.keepWhitespace ? value : value.trim();
}

export function buildPatch(fields: readonly FieldDef[], draft: Draft): { items: PatchItem[]; invalid: FieldDef[] } {
  const items: PatchItem[] = [];
  const invalid: FieldDef[] = [];
  for (const field of fields) {
    const wire = toWire(field, draft[fieldId(field)] ?? "");
    if (wire === undefined) invalid.push(field);
    else items.push({ namespace: field.ns, key: field.key, value: wire });
  }
  return { items, invalid };
}

/** After a save: reset the saved group, keep other groups' unsaved edits. */
export function rebase(prevSaved: Draft, prevDraft: Draft, nextSaved: Draft, reset: ReadonlySet<string>): Draft {
  const out: Draft = {};
  for (const id of Object.keys(nextSaved)) {
    out[id] = reset.has(id) || prevDraft[id] === prevSaved[id] ? (nextSaved[id] as string) : (prevDraft[id] as string);
  }
  return out;
}

export function boolOf(draft: Draft, id: string, fallback = false): boolean {
  const v = draft[id];
  if (v === undefined || v === "") return fallback;
  return v === "true";
}

export function setBool(draft: Draft, id: string, value: boolean): Draft {
  return { ...draft, [id]: value ? "true" : "false" };
}

/** Strict integer text: digits only. */
export function isDigits(value: string): boolean {
  return /^\d+$/.test(value.trim());
}

export function toInt(value: string, fallback: number): number {
  const text = value.trim();
  return /^-?\d+$/.test(text) ? Number(text) : fallback;
}

export function splitList(value: string): string[] {
  return value
    .split(/[\n,]/)
    .map((x) => x.trim())
    .filter(Boolean);
}

/** Case-insensitive de-duplication that keeps the first spelling and the order. */
export function dedupeCaseInsensitive(items: readonly string[]): string[] {
  const seen = new Set<string>();
  const out: string[] = [];
  for (const item of items) {
    const k = item.toLowerCase();
    if (seen.has(k)) continue;
    seen.add(k);
    out.push(item);
  }
  return out;
}
