/**
 * The expression language of interactive blocks. Never uses eval or dynamic
 * function construction: sources are parsed into a tree and walked with a step
 * budget, so a model-written formula cannot run code or loop forever.
 */

export type Value = null | boolean | number | string | Value[] | { [key: string]: Value };

export class ExprError extends Error {
  constructor(
    message: string,
    readonly position = -1,
  ) {
    super(message);
    this.name = "ExprError";
  }
}

export const MAX_SOURCE = 512;
export const DEFAULT_STEPS = 100_000;
export const MAX_RANGE_POINTS = 1000;
const FORBIDDEN_PROPS = new Set(["__proto__", "constructor", "prototype"]);

export interface Budget {
  steps: number;
}

// --- tokens -------------------------------------------------------------------------------------

type Tok =
  | { t: "num"; v: number; at: number }
  | { t: "str"; v: string; at: number }
  | { t: "id"; v: string; at: number }
  | { t: "op"; v: string; at: number }
  | { t: "end"; at: number };

const OPS3 = ["===", "!=="];
const OPS2 = ["==", "!=", "<=", ">=", "&&", "||"];
const OPS1 = "+-*/%^!?:<>()[],.";

function tokenize(src: string): Tok[] {
  const out: Tok[] = [];
  let i = 0;
  while (i < src.length) {
    const c = src[i] as string;
    if (/\s/.test(c)) {
      i++;
      continue;
    }
    if (/\d/.test(c) || (c === "." && /\d/.test(src[i + 1] ?? ""))) {
      const m = /^(?:\d+\.?\d*|\.\d+)(?:[eE][+-]?\d+)?/.exec(src.slice(i)) as RegExpExecArray;
      out.push({ t: "num", v: Number(m[0]), at: i });
      i += m[0].length;
      continue;
    }
    if (c === '"' || c === "'") {
      let s = "";
      let j = i + 1;
      let closed = false;
      while (j < src.length) {
        const d = src[j] as string;
        if (d === "\\") {
          const n = src[j + 1];
          if (n === undefined) break;
          s += n === "n" ? "\n" : n === "t" ? "\t" : n;
          j += 2;
          continue;
        }
        if (d === c) {
          closed = true;
          j++;
          break;
        }
        s += d;
        j++;
      }
      if (!closed) throw new ExprError("unterminated string", i);
      out.push({ t: "str", v: s, at: i });
      i = j;
      continue;
    }
    if (/[A-Za-z_]/.test(c)) {
      const m = /^[A-Za-z_][A-Za-z0-9_]*/.exec(src.slice(i)) as RegExpExecArray;
      out.push({ t: "id", v: m[0], at: i });
      i += m[0].length;
      continue;
    }
    const three = OPS3.find((o) => src.startsWith(o, i));
    if (three) {
      out.push({ t: "op", v: three === "===" ? "==" : "!=", at: i });
      i += 3;
      continue;
    }
    const two = OPS2.find((o) => src.startsWith(o, i));
    if (two) {
      out.push({ t: "op", v: two, at: i });
      i += 2;
      continue;
    }
    if (OPS1.includes(c)) {
      out.push({ t: "op", v: c, at: i });
      i++;
      continue;
    }
    throw new ExprError(`unexpected character ${JSON.stringify(c)}`, i);
  }
  out.push({ t: "end", at: src.length });
  return out;
}

// --- tree ----------------------------------------------------------------------------------------------

export type Node =
  | { k: "num"; v: number }
  | { k: "str"; v: string }
  | { k: "bool"; v: boolean }
  | { k: "null" }
  | { k: "list"; items: Node[] }
  | { k: "var"; name: string }
  | { k: "un"; op: "!" | "-"; arg: Node }
  | { k: "bin"; op: string; l: Node; r: Node }
  | { k: "cond"; c: Node; a: Node; b: Node }
  | { k: "member"; obj: Node; name: string }
  | { k: "index"; obj: Node; index: Node }
  | { k: "call"; name: string; args: Node[] };

class Parser {
  i = 0;
  constructor(readonly toks: Tok[]) {}
  peek(): Tok {
    return this.toks[this.i] as Tok;
  }
  next(): Tok {
    return this.toks[this.i++] as Tok;
  }
  isOp(v: string): boolean {
    const t = this.peek();
    return t.t === "op" && t.v === v;
  }
  eat(v: string): boolean {
    if (this.isOp(v)) {
      this.i++;
      return true;
    }
    return false;
  }
  expect(v: string): void {
    if (!this.eat(v)) this.fail();
  }
  fail(): never {
    const t = this.peek();
    const shown = t.t === "end" ? "end of input" : t.t === "op" ? `"${t.v}"` : t.t === "num" ? String(t.v) : t.t === "str" ? JSON.stringify(t.v) : t.v;
    throw new ExprError(`unexpected ${shown}`, t.at);
  }

  parse(): Node {
    const n = this.cond();
    if (this.peek().t !== "end") this.fail();
    return n;
  }

  cond(): Node {
    const c = this.or();
    if (this.eat("?")) {
      const a = this.cond();
      this.expect(":");
      const b = this.cond();
      return { k: "cond", c, a, b };
    }
    return c;
  }
  or(): Node {
    let l = this.and();
    while (this.eat("||")) l = { k: "bin", op: "||", l, r: this.and() };
    return l;
  }
  and(): Node {
    let l = this.eq();
    while (this.eat("&&")) l = { k: "bin", op: "&&", l, r: this.eq() };
    return l;
  }
  eq(): Node {
    let l = this.rel();
    for (;;) {
      if (this.eat("==")) l = { k: "bin", op: "==", l, r: this.rel() };
      else if (this.eat("!=")) l = { k: "bin", op: "!=", l, r: this.rel() };
      else return l;
    }
  }
  rel(): Node {
    let l = this.add();
    for (;;) {
      const op = ["<=", ">=", "<", ">"].find((o) => this.isOp(o));
      if (!op) return l;
      this.i++;
      l = { k: "bin", op, l, r: this.add() };
    }
  }
  add(): Node {
    let l = this.mul();
    for (;;) {
      if (this.eat("+")) l = { k: "bin", op: "+", l, r: this.mul() };
      else if (this.eat("-")) l = { k: "bin", op: "-", l, r: this.mul() };
      else return l;
    }
  }
  mul(): Node {
    let l = this.unary();
    for (;;) {
      const op = ["*", "/", "%"].find((o) => this.isOp(o));
      if (!op) return l;
      this.i++;
      l = { k: "bin", op, l, r: this.unary() };
    }
  }
  unary(): Node {
    if (this.eat("!")) return { k: "un", op: "!", arg: this.unary() };
    if (this.eat("-")) return { k: "un", op: "-", arg: this.unary() };
    if (this.eat("+")) return this.unary();
    return this.pow();
  }
  pow(): Node {
    const base = this.postfix();
    if (this.eat("^")) return { k: "bin", op: "^", l: base, r: this.unary() };
    return base;
  }
  postfix(): Node {
    let n = this.primary();
    for (;;) {
      if (this.eat(".")) {
        const t = this.next();
        if (t.t !== "id") {
          this.i--;
          this.fail();
        }
        n = { k: "member", obj: n, name: t.v };
      } else if (this.eat("[")) {
        const index = this.cond();
        this.expect("]");
        n = { k: "index", obj: n, index };
      } else return n;
    }
  }
  primary(): Node {
    const t = this.next();
    if (t.t === "num") return { k: "num", v: t.v };
    if (t.t === "str") return { k: "str", v: t.v };
    if (t.t === "id") {
      if (t.v === "true") return { k: "bool", v: true };
      if (t.v === "false") return { k: "bool", v: false };
      if (t.v === "null") return { k: "null" };
      if (this.isOp("(")) {
        this.i++;
        const args: Node[] = [];
        if (!this.eat(")")) {
          for (;;) {
            args.push(this.cond());
            if (this.eat(")")) break;
            this.expect(",");
          }
        }
        if (!(t.v in FUNCS)) throw new ExprError(`unknown function ${t.v}`, t.at);
        return { k: "call", name: t.v, args };
      }
      return { k: "var", name: t.v };
    }
    if (t.t === "op" && t.v === "(") {
      const n = this.cond();
      this.expect(")");
      return n;
    }
    if (t.t === "op" && t.v === "[") {
      const items: Node[] = [];
      if (!this.eat("]")) {
        for (;;) {
          items.push(this.cond());
          if (this.eat("]")) break;
          this.expect(",");
        }
      }
      return { k: "list", items };
    }
    this.i--;
    return this.fail();
  }
}

// --- helpers --------------------------------------------------------------------------------------------

export function truthy(v: Value): boolean {
  if (v === null || v === false || v === 0 || v === "") return false;
  if (typeof v === "number" && Number.isNaN(v)) return false;
  return true;
}

export function deepEqual(a: Value, b: Value): boolean {
  if (a === b) return true;
  if (typeof a === "number" && typeof b === "number") return Number.isNaN(a) && Number.isNaN(b);
  if (Array.isArray(a) || Array.isArray(b)) {
    if (!Array.isArray(a) || !Array.isArray(b) || a.length !== b.length) return false;
    return a.every((x, i) => deepEqual(x, b[i] as Value));
  }
  if (a && b && typeof a === "object" && typeof b === "object") {
    const ka = Object.keys(a);
    const kb = Object.keys(b);
    return ka.length === kb.length && ka.every((k) => k in b && deepEqual(a[k] as Value, (b as Record<string, Value>)[k] as Value));
  }
  return false;
}

function show(v: Value): string {
  if (v === null) return "";
  if (typeof v === "string") return v;
  if (typeof v === "number" || typeof v === "boolean") return String(v);
  try {
    return JSON.stringify(v);
  } catch {
    return "";
  }
}

function num(v: Value, op: string): number {
  if (typeof v !== "number") throw new ExprError(`${op} needs numbers`);
  return v;
}

function spend(b: Budget, n = 1): void {
  b.steps -= n;
  if (b.steps < 0) throw new ExprError("the formula is too expensive to evaluate");
}

// --- functions --------------------------------------------------------------------------------------------

type Fn = (args: Value[], budget: Budget) => Value;

function arity(name: string, args: Value[], min: number, max = min): void {
  if (args.length < min || args.length > max) throw new ExprError(`${name} takes ${min === max ? min : `${min} to ${max}`} argument(s)`);
}

const math1 = (name: string, f: (x: number) => number): Fn => (args) => {
  arity(name, args, 1);
  return f(num(args[0] as Value, name));
};

function listArg(name: string, v: Value | undefined): Value[] {
  if (!Array.isArray(v)) throw new ExprError(`${name} needs a list`);
  return v;
}

function round10(n: number): number {
  return Number(n.toFixed(10));
}

const FUNCS: Record<string, Fn> = {
  abs: math1("abs", Math.abs),
  sqrt: math1("sqrt", Math.sqrt),
  cbrt: math1("cbrt", Math.cbrt),
  floor: math1("floor", Math.floor),
  ceil: math1("ceil", Math.ceil),
  sign: math1("sign", Math.sign),
  log: math1("log", Math.log),
  ln: math1("ln", Math.log),
  log10: math1("log10", Math.log10),
  log2: math1("log2", Math.log2),
  exp: math1("exp", Math.exp),
  sin: math1("sin", Math.sin),
  cos: math1("cos", Math.cos),
  tan: math1("tan", Math.tan),
  asin: math1("asin", Math.asin),
  acos: math1("acos", Math.acos),
  atan: math1("atan", Math.atan),
  sinh: math1("sinh", Math.sinh),
  cosh: math1("cosh", Math.cosh),
  tanh: math1("tanh", Math.tanh),
  pow: (args) => {
    arity("pow", args, 2);
    return Math.pow(num(args[0] as Value, "pow"), num(args[1] as Value, "pow"));
  },
  min: (args, b) => extreme("min", args, b, Math.min),
  max: (args, b) => extreme("max", args, b, Math.max),
  round: (args) => {
    arity("round", args, 1, 2);
    const v = num(args[0] as Value, "round");
    const d = args.length > 1 ? Math.trunc(num(args[1] as Value, "round")) : 0;
    const f = 10 ** Math.min(Math.max(d, 0), 15);
    return Math.round(v * f) / f;
  },
  fixed: (args) => {
    arity("fixed", args, 1, 2);
    const v = num(args[0] as Value, "fixed");
    const d = args.length > 1 ? Math.min(10, Math.max(0, Math.trunc(num(args[1] as Value, "fixed")))) : 0;
    return Number.isFinite(v) ? v.toFixed(d) : "—";
  },
  clamp: (args) => {
    arity("clamp", args, 3);
    const [v, lo, hi] = args.map((a) => num(a as Value, "clamp")) as [number, number, number];
    return Math.min(Math.max(v, lo), hi);
  },
  range: (args, b) => {
    arity("range", args, 2, 3);
    const start = num(args[0] as Value, "range");
    const end = num(args[1] as Value, "range");
    const step = args.length > 2 ? num(args[2] as Value, "range") : end >= start ? 1 : -1;
    if (!Number.isFinite(start) || !Number.isFinite(end) || !Number.isFinite(step) || step === 0) throw new ExprError("range needs a non-zero step");
    if ((end - start) * step < 0) throw new ExprError("range step points the wrong way");
    const count = Math.floor((end - start) / step + 1e-9) + 1;
    if (count > MAX_RANGE_POINTS) throw new ExprError(`range is limited to ${MAX_RANGE_POINTS} points`);
    spend(b, count);
    const out: number[] = [];
    for (let k = 0; k < count; k++) out.push(round10(start + k * step));
    return out;
  },
  sum: (args, b) => {
    arity("sum", args, 1);
    const list = listArg("sum", args[0]);
    spend(b, list.length);
    return list.reduce<number>((acc, x) => acc + num(x, "sum"), 0);
  },
  avg: (args, b) => {
    arity("avg", args, 1);
    const list = listArg("avg", args[0]);
    if (list.length === 0) throw new ExprError("avg of an empty list");
    spend(b, list.length);
    return list.reduce<number>((acc, x) => acc + num(x, "avg"), 0) / list.length;
  },
  len: (args) => {
    arity("len", args, 1);
    const v = args[0] as Value;
    if (typeof v === "string") return Array.from(v).length;
    if (Array.isArray(v)) return v.length;
    throw new ExprError("len needs a list or text");
  },
  contains: (args, b) => {
    arity("contains", args, 2);
    const [hay, needle] = args as [Value, Value];
    if (typeof hay === "string") return typeof needle === "string" ? hay.includes(needle) : hay.includes(show(needle));
    if (Array.isArray(hay)) {
      spend(b, hay.length);
      return hay.some((x) => deepEqual(x, needle));
    }
    throw new ExprError("contains needs text or a list");
  },
};

function extreme(name: string, args: Value[], b: Budget, f: (...n: number[]) => number): Value {
  let values: Value[];
  if (args.length === 1 && Array.isArray(args[0])) {
    values = args[0];
    spend(b, values.length);
  } else {
    if (args.length > 64) throw new ExprError(`${name} takes at most 64 numbers`);
    values = args;
  }
  if (values.length === 0) throw new ExprError(`${name} of an empty list`);
  return f(...values.map((v) => num(v, name)));
}

// --- evaluation -------------------------------------------------------------------------------------------------

export type Scope = Readonly<Record<string, Value | undefined>>;

function lookup(name: string, scope: Scope): Value {
  if (Object.prototype.hasOwnProperty.call(scope, name) && scope[name] !== undefined) return scope[name] as Value;
  if (name === "pi") return Math.PI;
  if (name === "e") return Math.E;
  throw new ExprError(`unknown variable ${name}`);
}

function evalNode(n: Node, scope: Scope, b: Budget): Value {
  spend(b);
  switch (n.k) {
    case "num":
    case "str":
    case "bool":
      return n.v;
    case "null":
      return null;
    case "list":
      return n.items.map((x) => evalNode(x, scope, b));
    case "var":
      return lookup(n.name, scope);
    case "un": {
      const v = evalNode(n.arg, scope, b);
      return n.op === "!" ? !truthy(v) : -num(v, "-");
    }
    case "cond":
      return truthy(evalNode(n.c, scope, b)) ? evalNode(n.a, scope, b) : evalNode(n.b, scope, b);
    case "member": {
      const o = evalNode(n.obj, scope, b);
      if (FORBIDDEN_PROPS.has(n.name)) throw new ExprError(`${n.name} is not allowed`);
      if (n.name === "length" && (Array.isArray(o) || typeof o === "string")) return o.length;
      if (o && typeof o === "object" && !Array.isArray(o) && Object.prototype.hasOwnProperty.call(o, n.name)) return (o as Record<string, Value>)[n.name] as Value;
      throw new ExprError(`unknown property ${n.name}`);
    }
    case "index": {
      const o = evalNode(n.obj, scope, b);
      const i = evalNode(n.index, scope, b);
      if (Array.isArray(o)) {
        if (typeof i !== "number" || !Number.isInteger(i) || i < 0 || i >= o.length) throw new ExprError("list index out of range");
        return o[i] as Value;
      }
      if (o && typeof o === "object" && typeof i === "string") {
        if (FORBIDDEN_PROPS.has(i)) throw new ExprError(`${i} is not allowed`);
        if (Object.prototype.hasOwnProperty.call(o, i)) return (o as Record<string, Value>)[i] as Value;
        throw new ExprError(`unknown property ${i}`);
      }
      throw new ExprError("cannot read an element here");
    }
    case "call": {
      const args = n.args.map((a) => evalNode(a, scope, b));
      return (FUNCS[n.name] as Fn)(args, b);
    }
    case "bin": {
      if (n.op === "&&") {
        const l = evalNode(n.l, scope, b);
        return truthy(l) ? evalNode(n.r, scope, b) : l;
      }
      if (n.op === "||") {
        const l = evalNode(n.l, scope, b);
        return truthy(l) ? l : evalNode(n.r, scope, b);
      }
      const l = evalNode(n.l, scope, b);
      const r = evalNode(n.r, scope, b);
      switch (n.op) {
        case "+":
          if (typeof l === "string" || typeof r === "string") return show(l) + show(r);
          return num(l, "+") + num(r, "+");
        case "-":
          return num(l, "-") - num(r, "-");
        case "*":
          return num(l, "*") * num(r, "*");
        case "/":
          return num(l, "/") / num(r, "/");
        case "%":
          return num(l, "%") % num(r, "%");
        case "^":
          return Math.pow(num(l, "^"), num(r, "^"));
        case "==":
          return deepEqual(l, r);
        case "!=":
          return !deepEqual(l, r);
        default: {
          if (typeof l === "string" && typeof r === "string") {
            return n.op === "<" ? l < r : n.op === "<=" ? l <= r : n.op === ">" ? l > r : l >= r;
          }
          const a = num(l, n.op);
          const c = num(r, n.op);
          return n.op === "<" ? a < c : n.op === "<=" ? a <= c : n.op === ">" ? a > c : a >= c;
        }
      }
    }
  }
}

// --- public api ------------------------------------------------------------------------------------------------------

export interface Compiled {
  source: string;
  ast: Node;
  /** Names the formula reads as variables (the constants pi and e excluded). */
  vars: string[];
  evaluate: (scope?: Scope, budget?: Budget) => Value;
}

function collectVars(n: Node, out: Set<string>): void {
  switch (n.k) {
    case "var":
      if (n.name !== "pi" && n.name !== "e") out.add(n.name);
      return;
    case "list":
      n.items.forEach((x) => collectVars(x, out));
      return;
    case "un":
      collectVars(n.arg, out);
      return;
    case "bin":
      collectVars(n.l, out);
      collectVars(n.r, out);
      return;
    case "cond":
      collectVars(n.c, out);
      collectVars(n.a, out);
      collectVars(n.b, out);
      return;
    case "member":
      collectVars(n.obj, out);
      return;
    case "index":
      collectVars(n.obj, out);
      collectVars(n.index, out);
      return;
    case "call":
      n.args.forEach((x) => collectVars(x, out));
      return;
    default:
      return;
  }
}

export function compile(source: string): Compiled {
  if (source.length > MAX_SOURCE) throw new ExprError("the formula is too long");
  const ast = new Parser(tokenize(source)).parse();
  const vars = new Set<string>();
  collectVars(ast, vars);
  return {
    source,
    ast,
    vars: [...vars],
    evaluate: (scope = {}, budget = { steps: DEFAULT_STEPS }) => evalNode(ast, scope, budget),
  };
}

export function evaluate(source: string, scope: Scope = {}, steps = DEFAULT_STEPS): Value {
  return compile(source).evaluate(scope, { steps });
}

function couldBeNumber(n: Node): boolean {
  switch (n.k) {
    case "num":
    case "var":
    case "member":
    case "index":
      return true;
    case "un":
      return n.op === "-";
    case "bin":
      return ["+", "-", "*", "/", "%", "^"].includes(n.op);
    case "cond":
      return couldBeNumber(n.a) || couldBeNumber(n.b);
    case "call":
      return !["fixed", "contains", "range"].includes(n.name);
    default:
      return false;
  }
}

/** A formula that must yield a number: rejects comparisons, logic, lists and text at compile time. */
export function compileNumeric(source: string): Compiled {
  const c = compile(source);
  if (!couldBeNumber(c.ast)) throw new ExprError("only numeric formulas are allowed here");
  return c;
}

export function evalNumeric(c: Compiled, vars: Record<string, number>, budget?: Budget): number {
  const v = c.evaluate(vars, budget);
  if (typeof v !== "number") throw new ExprError("the formula did not produce a number");
  return v;
}
