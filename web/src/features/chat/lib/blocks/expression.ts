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
