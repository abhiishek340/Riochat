/**
 * A forgiving JSON reader for streamed block bodies. It reads any prefix of a
 * document and says how much of it is finished.
 *
 * Tolerated: trailing commas, // and block comments, single-quoted strings,
 * unquoted keys, and unescaped double quotes inside strings (a quote ends a
 * string only when the next non-space character is , } ] : or the end).
 */

export class LenientJsonError extends Error {
  constructor(
    message: string,
    readonly position: number,
  ) {
    super(message);
    this.name = "LenientJsonError";
  }
}

export interface LenientResult {
  /** What was built so far (may be undefined for an empty or barely started body). */
  value: unknown;
  /** The top-level value is finished. */
  complete: boolean;
  /** Objects and arrays whose closing bracket was read. */
  closed: WeakSet<object>;
  /** For a container cut off mid-way: the key (or index) of the member that was cut. */
  cut: WeakMap<object, string | number>;
}

interface Read {
  value: unknown;
  done: boolean;
}

const KEY_START = /[A-Za-z_$]/;
const KEY_PART = /[\w$-]/;

class Reader {
  pos = 0;
  readonly closed = new WeakSet<object>();
  readonly cut = new WeakMap<object, string | number>();

  constructor(readonly src: string) {}

  get eof(): boolean {
    return this.pos >= this.src.length;
  }

  skip(): void {
    for (;;) {
      const c = this.src[this.pos];
      if (c === undefined) return;
      if (/\s/.test(c)) {
        this.pos++;
      } else if (c === "/" && this.src[this.pos + 1] === "/") {
        const nl = this.src.indexOf("\n", this.pos);
        this.pos = nl < 0 ? this.src.length : nl + 1;
      } else if (c === "/" && this.src[this.pos + 1] === "*") {
        const end = this.src.indexOf("*/", this.pos + 2);
        this.pos = end < 0 ? this.src.length : end + 2;
      } else if (c === "/" && this.pos + 1 >= this.src.length) {
        // a lone slash at the end may be the start of a comment
        this.pos = this.src.length;
      } else return;
    }
  }

  fail(message: string): never {
    throw new LenientJsonError(message, this.pos);
  }

  value(): Read {
    this.skip();
    const c = this.src[this.pos];
    if (c === undefined) return { value: undefined, done: false };
    if (c === "{") return this.object();
    if (c === "[") return this.array();
    if (c === '"' || c === "'") return this.string(c);
    if (c === "-" || (c >= "0" && c <= "9")) return this.number();
    return this.literal();
  }

  /** A quote closes the string only if what follows could continue a document. */
  private closes(at: number): boolean {
    let i = at + 1;
    while (i < this.src.length && /\s/.test(this.src[i] as string)) i++;
    if (i >= this.src.length) return true;
    return ",}]:".includes(this.src[i] as string);
  }

  string(quote: string): Read {
    let out = "";
    let i = this.pos + 1;
    while (i < this.src.length) {
      const c = this.src[i] as string;
      if (c === "\\") {
        const n = this.src[i + 1];
        if (n === undefined) {
          this.pos = this.src.length;
          return { value: out, done: false };
        }
        if (n === "u") {
          const hex = this.src.slice(i + 2, i + 6);
          if (hex.length < 4 && i + 6 > this.src.length) {
            this.pos = this.src.length;
            return { value: out, done: false };
          }
          if (/^[0-9a-fA-F]{4}$/.test(hex)) {
            out += String.fromCharCode(parseInt(hex, 16));
            i += 6;
            continue;
          }
          out += "u";
          i += 2;
          continue;
        }
        const map: Record<string, string> = { n: "\n", t: "\t", r: "\r", b: "\b", f: "\f", "/": "/", "\\": "\\", '"': '"', "'": "'" };
        out += map[n] ?? n;
        i += 2;
        continue;
      }
      if (c === quote && this.closes(i)) {
        this.pos = i + 1;
        return { value: out, done: true };
      }
      out += c;
      i++;
    }
    this.pos = this.src.length;
    return { value: out, done: false };
  }

  number(): Read {
    const m = /^-?\d*(?:\.\d*)?(?:[eE][+-]?\d*)?/.exec(this.src.slice(this.pos)) as RegExpExecArray;
    const text = m[0];
    this.pos += text.length;
    // A number that touches the end of the input may still grow.
    if (this.eof) return { value: Number.isFinite(Number(text)) && text !== "-" ? Number(text) : undefined, done: false };
    const n = Number(text);
    if (!Number.isFinite(n) || /[eE.+-]$/.test(text)) this.fail("invalid number");
    return { value: n, done: true };
  }

  literal(): Read {
    for (const [word, value] of [["true", true], ["false", false], ["null", null]] as const) {
      const rest = this.src.slice(this.pos, this.pos + word.length);
      if (rest === word) {
        this.pos += word.length;
        return { value, done: true };
      }
      if (this.pos + rest.length >= this.src.length && word.startsWith(rest) && rest.length > 0) {
        this.pos = this.src.length;
        return { value: undefined, done: false };
      }
    }
    return this.fail(`unexpected character ${JSON.stringify(this.src[this.pos])}`);
  }

  key(): { key: string | null; done: boolean } {
    const c = this.src[this.pos] as string;
    if (c === '"' || c === "'") {
      const r = this.string(c);
      return { key: r.value as string, done: r.done };
    }
    if (KEY_START.test(c)) {
      let i = this.pos;
      while (i < this.src.length && KEY_PART.test(this.src[i] as string)) i++;
      const key = this.src.slice(this.pos, i);
      this.pos = i;
      return { key, done: i < this.src.length };
    }
    return this.fail(`unexpected character ${JSON.stringify(c)}`);
  }

  object(): Read {
    const obj: Record<string, unknown> = {};
    this.pos++;
    for (;;) {
      this.skip();
      if (this.eof) return { value: obj, done: false };
      const c = this.src[this.pos] as string;
      if (c === "}") {
        this.pos++;
        this.closed.add(obj);
        return { value: obj, done: true };
      }
      if (c === ",") {
        this.pos++;
        continue;
      }
      const k = this.key();
      if (k.key === null) return { value: obj, done: false };
      if (!k.done) {
        // The key itself is still being typed.
        this.cut.set(obj, k.key);
        return { value: obj, done: false };
      }
      this.skip();
      if (this.eof) {
        this.cut.set(obj, k.key);
        return { value: obj, done: false };
      }
      if (this.src[this.pos] !== ":") this.fail("expected a colon");
      this.pos++;
      const v = this.value();
      if (v.value !== undefined) obj[k.key] = v.value;
      if (!v.done) {
        this.cut.set(obj, k.key);
        return { value: obj, done: false };
      }
      this.skip();
      if (this.eof) return { value: obj, done: false };
      const sep = this.src[this.pos] as string;
      if (sep === ",") this.pos++;
      else if (sep !== "}") this.fail(`unexpected character ${JSON.stringify(sep)}`);
    }
  }

  array(): Read {
    const arr: unknown[] = [];
    this.pos++;
    for (;;) {
      this.skip();
      if (this.eof) return { value: arr, done: false };
      const c = this.src[this.pos] as string;
      if (c === "]") {
        this.pos++;
        this.closed.add(arr);
        return { value: arr, done: true };
      }
      if (c === ",") {
        this.pos++;
        continue;
      }
      const index = arr.length;
      const v = this.value();
      if (v.value !== undefined) arr.push(v.value);
      if (!v.done) {
        this.cut.set(arr, index);
        return { value: arr, done: false };
      }
      this.skip();
      if (this.eof) return { value: arr, done: false };
      const sep = this.src[this.pos] as string;
      if (sep === ",") this.pos++;
      else if (sep !== "]") this.fail(`unexpected character ${JSON.stringify(sep)}`);
    }
  }
}

/** Reads a (possibly partial) document. Syntax errors throw LenientJsonError. */
export function readLenient(src: string): LenientResult {
  const r = new Reader(src);
  r.skip();
  if (r.eof) return { value: undefined, complete: false, closed: r.closed, cut: r.cut };
  const res = r.value();
  return { value: res.value, complete: res.done, closed: r.closed, cut: r.cut };
}
