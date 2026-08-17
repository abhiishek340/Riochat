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

function evaluateComputed(block: Block, state: Record<string, Value>, budget: Budget, issues: Issue[]): Record<string, Value> {
  const entries = new Map<string, ComputedEntry>();
  let count = 0;
  for (const [name, raw] of Object.entries(block.computed ?? {})) {
    if (!NAME.test(name)) {
      issues.push({ path: `computed.${name}`, message: "invalid name" });
      continue;
    }
    if (name in state && block.state && name in block.state) {
      issues.push({ path: `computed.${name}`, message: "also declared as state" });
      continue;
    }
    if (++count > LIMITS.computed) {
      issues.push({ path: "computed", message: `at most ${LIMITS.computed} computed values` });
      break;
    }
    if (typeof raw === "string") {
      try {
        entries.set(name, { name, expr: compile(unwrapBraces(raw)), constant: undefined });
      } catch (e) {
        issues.push({ path: `computed.${name}`, message: e instanceof ExprError ? e.message : "invalid formula" });
        entries.set(name, { name, expr: null, constant: undefined });
      }
    } else if (isJsonValue(raw)) entries.set(name, { name, expr: null, constant: raw });
    else {
      issues.push({ path: `computed.${name}`, message: "constants must be plain JSON" });
      entries.set(name, { name, expr: null, constant: undefined });
    }
  }

  const values: Record<string, Value> = {};
  const status = new Map<string, "visiting" | "done" | "failed">();
  const visit = (name: string): void => {
    const st = status.get(name);
    if (st === "done" || st === "failed") return;
    const entry = entries.get(name) as ComputedEntry;
    if (st === "visiting") return;
    status.set(name, "visiting");
    if (entry.expr) {
      let cyclic = false;
      for (const dep of entry.expr.vars) {
        if (!entries.has(dep) || dep in state) continue;
        if (status.get(dep) === "visiting") {
          cyclic = true;
          issues.push({ path: `computed.${name}`, message: `cycle through ${dep}` });
          status.set(dep, "failed");
        } else visit(dep);
      }
      if (cyclic) {
        status.set(name, "failed");
        return;
      }
      try {
        values[name] = entry.expr.evaluate({ ...state, ...values }, budget);
        status.set(name, "done");
      } catch (e) {
        issues.push({ path: `computed.${name}`, message: e instanceof ExprError ? e.message : "evaluation failed" });
        status.set(name, "failed");
      }
    } else if (entry.constant !== undefined) {
      values[name] = entry.constant;
      status.set(name, "done");
    } else status.set(name, "failed");
  };
  for (const name of entries.keys()) visit(name);
  // A failed entry that was marked by a cycle must not keep a value.
  for (const [name, st] of status) if (st === "failed") delete values[name];
  return values;
}

// --- props -------------------------------------------------------------------------------------------------------

/** A template that reads per-point variables: evaluated later, once per point. */
export interface PointTemplate {
  __point: true;
  template: Template;
}

function isPoint(v: unknown): v is PointTemplate {
  return isRecord(v) && v.__point === true;
}

interface Ctx {
  values: Record<string, Value>;
  budget: Budget;
  issues: Issue[];
  pointVars: ReadonlySet<string>;
  nodes: number;
}

function resolveValue(v: unknown, path: string, ctx: Ctx): unknown {
  if (typeof v === "string") {
    let t: Template;
    try {
      t = parseTemplate(v);
    } catch (e) {
      ctx.issues.push({ path, message: e instanceof TemplateError ? e.message : "invalid template" });
      return undefined;
    }
    if (t.isStatic) return v;
    if (t.vars.some((x) => ctx.pointVars.has(x))) return { __point: true, template: t } satisfies PointTemplate;
    const r = resolveTemplate(t, ctx.values, ctx.budget);
    for (const m of r.issues) ctx.issues.push({ path, message: m });
    return r.value;
  }
  if (Array.isArray(v)) return v.map((x, i) => resolveValue(x, `${path}[${i}]`, ctx)).filter((x) => x !== undefined);
  if (isRecord(v)) {
    const out: Record<string, unknown> = {};
    for (const [k, x] of Object.entries(v)) {
      const r = resolveValue(x, `${path}.${k}`, ctx);
      if (r !== undefined) out[k] = r;
    }
    return out;
  }
  return v;
}

function evalPoint(p: PointTemplate, scope: Record<string, Value>, ctx: Ctx, path: string): Value | undefined {
  const r = resolveTemplate(p.template, scope, ctx.budget);
  if (r.issues.length > 0) ctx.issues.push({ path, message: r.issues[0] as string });
  return r.value;
}

function expandPoints(node: ResolvedNode, ctx: Ctx): void {
  const props = node.props;
  if (node.component === "chart" && Array.isArray(props.series)) {
    const xs = Array.isArray(props.x) ? (props.x as Value[]) : null;
    props.series = (props.series as Record<string, unknown>[]).map((s, si) => {
      if (!isPoint(s.data)) return s;
      const path = `${node.path}.series[${si}].data`;
      if (!xs) {
        ctx.issues.push({ path, message: "per-point formulas need x" });
        return { ...s, data: [] };
      }
      const data = xs.map((x, i) => {
        const v = evalPoint(s.data as PointTemplate, { ...ctx.values, x, i }, ctx, path);
        return typeof v === "number" && Number.isFinite(v) ? v : null;
      });
      return { ...s, data };
    });
  }
  if ("where" in props) {
    const where = props.where;
    const listKey = node.component === "data-table" ? "rows" : node.component === "card-grid" ? "items" : "";
    if (!listKey) {
      ctx.issues.push({ path: `${node.path}.where`, message: "where is only used by data-table and card-grid" });
      delete props.where;
    } else {
      const list = props[listKey];
      if (typeof where === "boolean") {
        if (!where && Array.isArray(list)) props[listKey] = [];
      } else if (isPoint(where) && Array.isArray(list)) {
        props[listKey] = list.filter((row, i) => evalPoint(where, { ...ctx.values, row: row as Value, i }, ctx, `${node.path}.where`) === true);
      } else if (where !== undefined) {
        ctx.issues.push({ path: `${node.path}.where`, message: "where must be true, false or a formula over row" });
      }
      delete props.where;
    }
  }
  // Point templates that nothing consumed are dropped.
  for (const [k, v] of Object.entries(props)) {
    if (isPoint(v)) {
      ctx.issues.push({ path: `${node.path}.${k}`, message: "per-point variables are not available here" });
      delete props[k];
    }
  }
}

function resolveNode(node: BlockNode, path: string, depth: number, ctx: Ctx): ResolvedNode | null {
  if (depth > LIMITS.depth) {
    ctx.issues.push({ path, message: `nested deeper than ${LIMITS.depth} levels` });
    return null;
  }
  if (++ctx.nodes > LIMITS.nodes) {
    if (ctx.nodes === LIMITS.nodes + 1) ctx.issues.push({ path, message: `at most ${LIMITS.nodes} nodes` });
    return null;
  }
  // visible: a boolean or an expression; invalid or failing hides the node.
  if (node.visible !== undefined) {
    let visible = false;
    if (typeof node.visible === "boolean") visible = node.visible;
    else if (typeof node.visible === "string") {
      try {
        const v = compile(unwrapBraces(node.visible)).evaluate(ctx.values, ctx.budget);
        visible = v === true || (v !== null && v !== false && v !== 0 && v !== "");
      } catch (e) {
        ctx.issues.push({ path: `${path}.visible`, message: e instanceof ExprError ? e.message : "invalid visibility formula" });
      }
    } else ctx.issues.push({ path: `${path}.visible`, message: "visible must be true, false or a formula" });
    if (!visible) return null;
  }
  const raw = RAW_PROPS[node.component] ?? [];
  const props: Record<string, unknown> = {};
  for (const [k, v] of Object.entries(node.props)) {
    if (raw.includes(k)) {
      props[k] = v;
      continue;
    }
    const r = resolveValue(v, `${path}.${k}`, ctx);
    if (r !== undefined) props[k] = r;
  }
  const children: ResolvedNode[] = [];
  node.children.forEach((c, i) => {
    const r = resolveNode(c, `${path}.children[${i}]`, depth + 1, ctx);
    if (r) children.push(r);
  });
  const out: ResolvedNode = { component: node.component, path, closed: node.closed, bind: implicitBind(node), props, children };
  expandPoints(out, ctx);
  return out;
}

export function resolveLive(block: Block, current: Record<string, Value>): Resolution {
  const issues: Issue[] = [];
  const budget: Budget = { steps: LIMITS.steps };
  const computed = evaluateComputed(block, current, budget, issues);
  const values: Record<string, Value> = { ...current, ...computed };
  const declaredNames = new Set([...Object.keys(block.state ?? {}), ...Object.keys(block.computed ?? {})]);
  const pointVars = new Set(POINT_VARS.filter((v) => !declaredNames.has(v)));
  const ctx: Ctx = { values, budget, issues, pointVars, nodes: 0 };
  const root = resolveNode(block.root, "root", 0, ctx);
  return { root, values, issues };
}

// --- button actions ---------------------------------------------------------------------------------------------------

export type ButtonAction = { kind: "ask"; text: string } | { kind: "copy"; text: string } | { kind: "set"; patch: Record<string, Value> } | null;

export function buttonAction(props: Record<string, unknown>, declared: Record<string, Value>): ButtonAction {
  if (typeof props.ask === "string" && props.ask.trim() !== "") return { kind: "ask", text: Array.from(props.ask.trim()).slice(0, LIMITS.ask).join("") };
  if (typeof props.copy === "string" && props.copy !== "") return { kind: "copy", text: props.copy };
  if (isRecord(props.set)) {
    const patch: Record<string, Value> = {};
    for (const [k, v] of Object.entries(props.set)) if (k in declared && isJsonValue(v)) patch[k] = v;
    if (Object.keys(patch).length > 0) return { kind: "set", patch };
  }
  return null;
}
