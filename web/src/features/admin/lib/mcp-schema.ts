/**
 * Reads the input schema of an MCP tool to decide which arguments can carry
 * an attached image. Only top-level properties that accept a string count.
 */
import { isPlainObject } from "./json";

export interface SchemaArgument {
  name: string;
  required: boolean;
}

const MAX_REF_DEPTH = 32;

type Json = Record<string, unknown>;

/** Resolves a local `#/...` reference; undefined when it cannot be followed. */
function resolveRef(root: Json, ref: string): Json | undefined {
  if (!ref.startsWith("#")) return undefined;
  const path = ref.slice(1).split("/").filter(Boolean).map((s) => s.replace(/~1/g, "/").replace(/~0/g, "~"));
  let node: unknown = root;
  for (const part of path) {
    if (!isPlainObject(node) && !Array.isArray(node)) return undefined;
    node = (node as Record<string, unknown>)[part];
  }
  return isPlainObject(node) ? node : undefined;
}

function acceptsString(root: Json, schema: unknown, depth: number): boolean {
  if (depth > MAX_REF_DEPTH || !isPlainObject(schema)) return false;
  if (typeof schema.$ref === "string") {
    const target = resolveRef(root, schema.$ref);
    return target ? acceptsString(root, target, depth + 1) : false;
  }
  const type = schema.type;
  if (type === "string") return true;
  if (Array.isArray(type) && type.includes("string")) return true;
  if (Array.isArray(schema.enum) && schema.enum.some((v) => typeof v === "string") && type === undefined) return true;
  if (typeof schema.const === "string" && type === undefined) return true;
  for (const key of ["anyOf", "oneOf"] as const) {
    const branches = schema[key];
    if (Array.isArray(branches) && branches.some((b) => acceptsString(root, b, depth + 1))) return true;
  }
  const all = schema.allOf;
  if (Array.isArray(all) && all.length > 0 && all.every((b) => acceptsString(root, b, depth + 1) || !hasType(b))) {
    return all.some((b) => acceptsString(root, b, depth + 1));
  }
  return false;
}

function hasType(schema: unknown): boolean {
  return isPlainObject(schema) && (schema.type !== undefined || schema.$ref !== undefined || schema.anyOf !== undefined || schema.oneOf !== undefined || schema.enum !== undefined);
}

/** Eligible arguments sorted by name. Invalid JSON or a non-object schema yields none. */
export function stringArguments(schemaJson: string | null | undefined): SchemaArgument[] {
  let root: unknown;
  try {
    root = JSON.parse(schemaJson || "{}");
  } catch {
    return [];
  }
  if (!isPlainObject(root) || !isPlainObject(root.properties)) return [];
  const required = new Set(Array.isArray(root.required) ? root.required.filter((r): r is string => typeof r === "string") : []);
  const out: SchemaArgument[] = [];
  for (const [name, schema] of Object.entries(root.properties)) {
    if (acceptsString(root, schema, 0)) out.push({ name, required: required.has(name) });
  }
  return out.sort((a, b) => a.name.localeCompare(b.name));
}

/** Names the schema marks as required, whatever their type. */
export function requiredArguments(schemaJson: string | null | undefined): string[] {
  try {
    const root: unknown = JSON.parse(schemaJson || "{}");
    if (!isPlainObject(root) || !Array.isArray(root.required)) return [];
    return root.required.filter((r): r is string => typeof r === "string");
  } catch {
    return [];
  }
}

export type AttachmentProblem = "imageArgument" | "promptArgument" | "promptSame" | "requiredUncovered";

export interface AttachmentDraft {
  mode: "none" | "image";
  imageArgument: string;
  promptEnabled: boolean;
  promptArgument: string;
}

/**
 * Save is blocked unless the image argument exists, the prompt argument (when
 * used) exists and differs, and every required argument is one of the mapped
 * ones. `none` is always valid.
 */
export function attachmentProblem(schemaJson: string, draft: AttachmentDraft): AttachmentProblem | null {
  if (draft.mode !== "image") return null;
  const eligible = new Set(stringArguments(schemaJson).map((a) => a.name));
  if (!draft.imageArgument || !eligible.has(draft.imageArgument)) return "imageArgument";
  if (draft.promptEnabled) {
    if (!draft.promptArgument || !eligible.has(draft.promptArgument)) return "promptArgument";
    if (draft.promptArgument === draft.imageArgument) return "promptSame";
  }
  const mapped = new Set([draft.imageArgument, draft.promptEnabled ? draft.promptArgument : ""]);
  for (const name of requiredArguments(schemaJson)) if (!mapped.has(name)) return "requiredUncovered";
  return null;
}
