/**
 * Live blocks: a block with state and computed values whose props are
 * templates. This module declares state, merges what the user changed, computes
 * values (with cycle detection) and resolves every node to plain props. It
 * never raises: model mistakes become issues.
 */
import type { Block, BlockNode } from "./block";
import { compile, ExprError, type Budget, type Compiled, type Value } from "./expression";
import { parseTemplate, resolveTemplate, TemplateError, type Template } from "./template";

export const PRIMITIVES = [
  "panel", "row", "stack", "grid", "card", "tabs", "collapse", "divider", "text", "stat", "badge", "progress", "callout", "formula", "scene",
  "slider", "select", "segmented", "switch", "input", "button",
] as const;
export const CONTROLS = new Set(["slider", "select", "segmented", "switch", "input", "tabs"]);
const RAW_PROPS: Record<string, readonly string[]> = { diff: ["before", "after"] };
const POINT_VARS = ["x", "i", "row"] as const;
const NAME = /^[A-Za-z_][A-Za-z0-9_]{0,63}$/;

export const LIMITS = { computed: 64, nodes: 300, depth: 8, steps: 200_000, stateDepth: 4, ask: 120 } as const;

export interface Issue {
  path: string;
  message: string;
}

export interface ResolvedNode {
  component: string;
  path: string;
  closed: boolean;
  bind: string | null;
  props: Record<string, unknown>;
  children: ResolvedNode[];
}

export interface Resolution {
  root: ResolvedNode | null;
  values: Record<string, Value>;
  issues: Issue[];
}

export function isLiveBlock(block: Block): boolean {
  return block.state !== undefined || block.computed !== undefined || (PRIMITIVES as readonly string[]).includes(block.component);
}

function isRecord(v: unknown): v is Record<string, unknown> {
  return typeof v === "object" && v !== null && !Array.isArray(v);
}

function jsonDepth(v: unknown, at = 0): number {
  if (Array.isArray(v)) return v.reduce<number>((m, x) => Math.max(m, jsonDepth(x, at + 1)), at + 1);
  if (isRecord(v)) return Object.values(v).reduce<number>((m, x) => Math.max(m, jsonDepth(x, at + 1)), at + 1);
  return at;
}

export function isJsonValue(v: unknown): v is Value {
  if (v === null || typeof v === "string" || typeof v === "boolean") return true;
  if (typeof v === "number") return Number.isFinite(v);
  if (Array.isArray(v)) return v.every(isJsonValue);
  if (isRecord(v)) return Object.values(v).every(isJsonValue);
  return false;
}

// --- state -------------------------------------------------------------------------------------------------------

function controlDefault(node: BlockNode): Value {
  const p = node.props;
  switch (node.component) {
    case "slider":
      return typeof p.min === "number" && Number.isFinite(p.min) ? p.min : 0;
    case "select":
    case "segmented": {
      const first = Array.isArray(p.options) ? p.options[0] : undefined;
      if (isRecord(first)) return (first.value as Value) ?? null;
      return (first as Value | undefined) ?? null;
    }
    case "tabs":
      return Array.isArray(p.items) ? ((p.items[0] as Value | undefined) ?? null) : null;
    case "switch":
      return false;
    case "input":
      return p.type === "number" ? 0 : "";
    default:
      return null;
  }
}

/** Name bound by `value: "{name}"` (exactly one identifier hole) when there is no `bind`. */
export function implicitBind(node: BlockNode): string | null {
  if (node.bind) return node.bind;
  if (!CONTROLS.has(node.component) || node.component === "tabs") return null;
  const v = node.props.value;
  if (typeof v !== "string") return null;
  const m = /^\{\s*([A-Za-z_][A-Za-z0-9_]*)\s*\}$/.exec(v);
  return m ? (m[1] as string) : null;
}

function walk(node: BlockNode, fn: (n: BlockNode) => void): void {
  fn(node);
  node.children.forEach((c) => walk(c, fn));
}

export interface Declared {
  state: Record<string, Value>;
  issues: Issue[];
}

export function declaredState(block: Block): Declared {
  const state: Record<string, Value> = {};
  const issues: Issue[] = [];
  for (const [name, value] of Object.entries(block.state ?? {})) {
    if (!NAME.test(name)) {
      issues.push({ path: `state.${name}`, message: "invalid state name" });
      continue;
    }
    if (!isJsonValue(value) || jsonDepth(value) > LIMITS.stateDepth) {
      issues.push({ path: `state.${name}`, message: "state values must be plain JSON, nested at most four levels" });
      continue;
    }
    state[name] = value;
  }
  // Bound controls get a default when the block did not declare their state.
  walk(block.root, (n) => {
    const name = implicitBind(n);
    if (name && NAME.test(name) && !(name in state)) state[name] = controlDefault(n);
  });
  return { state, issues };
}

function sameShape(a: Value, b: Value): boolean {
  if (a === null || b === null) return true;
  if (Array.isArray(a) || Array.isArray(b)) return Array.isArray(a) && Array.isArray(b);
  return typeof a === typeof b;
}

/** The user's stored values overlay declared state, only where the name, the JSON and the shape still fit. */
export function mergeState(declared: Record<string, Value>, stored: Record<string, unknown> | undefined): Record<string, Value> {
  const out: Record<string, Value> = { ...declared };
  if (!stored) return out;
  for (const [name, value] of Object.entries(stored)) {
    if (!(name in declared) || !isJsonValue(value)) continue;
    if (sameShape(declared[name] as Value, value)) out[name] = value;
  }
  return out;
}

function deepEqual(a: unknown, b: unknown): boolean {
  return JSON.stringify(a) === JSON.stringify(b);
}

/** What gets stored: declared names whose current value differs from the declared one. */
export function diffState(declared: Record<string, Value>, current: Record<string, Value>): Record<string, Value> {
  const out: Record<string, Value> = {};
  for (const name of Object.keys(declared)) if (name in current && !deepEqual(declared[name], current[name])) out[name] = current[name] as Value;
  return out;
}

// --- computed values ---------------------------------------------------------------------------------------------

interface ComputedEntry {
  name: string;
  expr: Compiled | null;
  constant: Value | undefined;
}

function unwrapBraces(src: string): string {
  const t = src.trim();
  if (t.startsWith("{") && t.endsWith("}") && !t.startsWith("{{")) return t.slice(1, -1);
  return t;
}
