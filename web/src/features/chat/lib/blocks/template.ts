/**
 * Templates: strings with `{expression}` holes, used for block props.
 */
import { compile, ExprError, type Budget, type Compiled, type Scope, type Value } from "./expression";

export class TemplateError extends Error {
  constructor(message: string) {
    super(message);
    this.name = "TemplateError";
  }
}

export type Part = { kind: "text"; text: string } | { kind: "hole"; source: string; expr: Compiled };

export interface Template {
  raw: string;
  parts: Part[];
  /** No holes: the string is passed through verbatim. */
  isStatic: boolean;
  /** The whole string is one hole, so the value keeps its type. */
  single: boolean;
  /** Variables read by all holes. */
  vars: string[];
}

/** Scans a template. Quotes inside a hole protect braces. */
export function parseTemplate(raw: string): Template {
  const parts: Part[] = [];
  let text = "";
  let holes = 0;
  let i = 0;
  const flush = () => {
    if (text) parts.push({ kind: "text", text });
    text = "";
  };
  // First pass: is there at least one hole? (decides whether doubled braces collapse)
  const scan = (collapse: boolean): void => {
    parts.length = 0;
    text = "";
    holes = 0;
    i = 0;
    while (i < raw.length) {
      const c = raw[i] as string;
      if (c === "{") {
        if (raw[i + 1] === "{") {
          text += collapse ? "{" : "{{";
          i += 2;
          continue;
        }
        let j = i + 1;
        let quote = "";
        let end = -1;
        for (; j < raw.length; j++) {
          const d = raw[j] as string;
          if (quote) {
            if (d === "\\") j++;
            else if (d === quote) quote = "";
            continue;
          }
          if (d === '"' || d === "'") quote = d;
          else if (d === "{") throw new TemplateError("a { inside a hole is not allowed");
          else if (d === "}") {
            end = j;
            break;
          }
        }
        if (end < 0) throw new TemplateError("unclosed {");
        const source = raw.slice(i + 1, end);
        flush();
        try {
          parts.push({ kind: "hole", source, expr: compile(source) });
        } catch (e) {
          throw new TemplateError(e instanceof ExprError ? e.message : "invalid expression");
        }
        holes++;
        i = end + 1;
        continue;
      }
      if (c === "}") {
        if (raw[i + 1] === "}") {
          text += collapse ? "}" : "}}";
          i += 2;
          continue;
        }
        throw new TemplateError("a lone }");
      }
      text += c;
      i++;
    }
    flush();
  };
  scan(false);
  if (holes > 0) scan(true);
  if (holes === 0) return { raw, parts: [{ kind: "text", text: raw }], isStatic: true, single: false, vars: [] };
  const vars = new Set<string>();
  for (const p of parts) if (p.kind === "hole") p.expr.vars.forEach((v) => vars.add(v));
  return { raw, parts, isStatic: false, single: parts.length === 1 && parts[0]?.kind === "hole", vars: [...vars] };
}

/** `1,234.57`: US-English grouping, at most two decimals; non-finite is a dash. */
export function formatNumber(n: number): string {
  if (!Number.isFinite(n)) return "—";
  return new Intl.NumberFormat("en-US", { maximumFractionDigits: 2 }).format(n);
}

export function stringify(v: Value): string {
  if (v === null) return "";
  if (typeof v === "number") return formatNumber(v);
  if (typeof v === "string") return v;
  if (typeof v === "boolean") return String(v);
  try {
    return JSON.stringify(v);
  } catch {
    return "";
  }
}

export interface Resolved {
  /** Undefined when the prop must be dropped (a single hole that failed). */
  value: Value | undefined;
  issues: string[];
}

export function resolveTemplate(t: Template, scope: Scope, budget: Budget): Resolved {
  if (t.isStatic) return { value: t.raw, issues: [] };
  const issues: string[] = [];
  const evalHole = (p: Extract<Part, { kind: "hole" }>): { ok: true; value: Value } | { ok: false } => {
    try {
      return { ok: true, value: p.expr.evaluate(scope, budget) };
    } catch (e) {
      issues.push(e instanceof ExprError ? e.message : "evaluation failed");
      return { ok: false };
    }
  };
  if (t.single) {
    const r = evalHole(t.parts[0] as Extract<Part, { kind: "hole" }>);
    return r.ok ? { value: r.value, issues } : { value: undefined, issues };
  }
  let out = "";
  for (const p of t.parts) {
    if (p.kind === "text") out += p.text;
    else {
      const r = evalHole(p);
      out += r.ok ? stringify(r.value) : "—";
    }
  }
  return { value: out, issues };
}
