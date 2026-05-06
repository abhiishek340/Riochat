/**
 * A small LaTeX-to-MathML converter for the formulas that show up in chat:
 * fractions, roots, scripts, big operators, delimiters, matrices, accents,
 * text, fonts and the usual symbols. Browsers render MathML natively. The
 * output only contains elements this module creates (all text is escaped), so
 * it is safe to insert as markup.
 */

export type MathNode =
  | { t: "row"; c: MathNode[] }
  | { t: "i"; v: string; variant?: string }
  | { t: "n"; v: string }
  | { t: "o"; v: string; stretchy?: boolean; fence?: boolean; large?: boolean; sep?: boolean }
  | { t: "text"; v: string }
  | { t: "space"; em: number }
  | { t: "frac"; num: MathNode; den: MathNode; line?: boolean }
  | { t: "sqrt"; body: MathNode; index?: MathNode }
  | { t: "sup"; base: MathNode; sup: MathNode }
  | { t: "sub"; base: MathNode; sub: MathNode }
  | { t: "subsup"; base: MathNode; sub: MathNode; sup: MathNode }
  | { t: "under"; base: MathNode; under: MathNode }
  | { t: "over"; base: MathNode; over: MathNode; accent?: boolean }
  | { t: "underover"; base: MathNode; under: MathNode; over: MathNode }
  | { t: "style"; variant: string; body: MathNode }
  | { t: "table"; rows: MathNode[][]; align?: string; lines?: boolean }
  | { t: "fenced"; open: string; close: string; body: MathNode }
  | { t: "error"; v: string };

const GREEK: Record<string, string> = {
  alpha: "α", beta: "β", gamma: "γ", delta: "δ", epsilon: "ϵ", varepsilon: "ε", zeta: "ζ", eta: "η", theta: "θ", vartheta: "ϑ", iota: "ι", kappa: "κ",
  lambda: "λ", mu: "μ", nu: "ν", xi: "ξ", omicron: "ο", pi: "π", varpi: "ϖ", rho: "ρ", varrho: "ϱ", sigma: "σ", varsigma: "ς", tau: "τ", upsilon: "υ",
  phi: "ϕ", varphi: "φ", chi: "χ", psi: "ψ", omega: "ω", Gamma: "Γ", Delta: "Δ", Theta: "Θ", Lambda: "Λ", Xi: "Ξ", Pi: "Π", Sigma: "Σ", Upsilon: "Υ",
  Phi: "Φ", Psi: "Ψ", Omega: "Ω",
};

const SYMBOLS: Record<string, string> = {
  pm: "±", mp: "∓", times: "×", div: "÷", cdot: "⋅", ast: "∗", star: "⋆", circ: "∘", bullet: "∙", oplus: "⊕", otimes: "⊗", odot: "⊙",
  leq: "≤", le: "≤", geq: "≥", ge: "≥", neq: "≠", ne: "≠", approx: "≈", equiv: "≡", sim: "∼", simeq: "≃", cong: "≅", propto: "∝", ll: "≪", gg: "≫",
  subset: "⊂", supset: "⊃", subseteq: "⊆", supseteq: "⊇", in: "∈", notin: "∉", ni: "∋", cup: "∪", cap: "∩", setminus: "∖", emptyset: "∅", varnothing: "∅",
  forall: "∀", exists: "∃", nexists: "∄", neg: "¬", lnot: "¬", land: "∧", wedge: "∧", lor: "∨", vee: "∨", implies: "⟹", iff: "⟺", impliedby: "⟸",
  to: "→", rightarrow: "→", leftarrow: "←", gets: "←", leftrightarrow: "↔", Rightarrow: "⇒", Leftarrow: "⇐", Leftrightarrow: "⇔", mapsto: "↦",
  uparrow: "↑", downarrow: "↓", longrightarrow: "⟶", longleftarrow: "⟵", hookrightarrow: "↪",
  infty: "∞", partial: "∂", nabla: "∇", angle: "∠", perp: "⊥", parallel: "∥", mid: "∣", ldots: "…", cdots: "⋯", vdots: "⋮", ddots: "⋱", dots: "…",
  prime: "′", degree: "°", hbar: "ℏ", ell: "ℓ", Re: "ℜ", Im: "ℑ", aleph: "ℵ", top: "⊤", bot: "⊥", therefore: "∴", because: "∵", square: "□", triangle: "△",
  langle: "⟨", rangle: "⟩", lceil: "⌈", rceil: "⌉", lfloor: "⌊", rfloor: "⌋", vert: "|", Vert: "‖", backslash: "\\", lbrace: "{", rbrace: "}",
  checkmark: "✓", dagger: "†", ddagger: "‡", S: "§", P: "¶", copyright: "©",
};

const BIG: Record<string, string> = { sum: "∑", prod: "∏", coprod: "∐", int: "∫", iint: "∬", iiint: "∭", oint: "∮", bigcup: "⋃", bigcap: "⋂", bigoplus: "⨁", bigotimes: "⨂", bigvee: "⋁", bigwedge: "⋀" };
const LIMITS_OPS = new Set(["sum", "prod", "coprod", "bigcup", "bigcap", "bigoplus", "bigotimes", "bigvee", "bigwedge", "lim", "max", "min", "sup", "inf", "limsup", "liminf", "det", "gcd", "Pr", "argmax", "argmin"]);
const FUNCTIONS = new Set([
  "sin", "cos", "tan", "cot", "sec", "csc", "arcsin", "arccos", "arctan", "sinh", "cosh", "tanh", "coth", "log", "ln", "lg", "exp", "lim", "limsup", "liminf",
  "max", "min", "sup", "inf", "det", "dim", "gcd", "ker", "deg", "hom", "arg", "Pr", "mod", "argmax", "argmin", "sgn", "trace", "rank",
]);
const SPACES: Record<string, number> = { ",": 0.17, ":": 0.22, ";": 0.28, "!": -0.17, quad: 1, qquad: 2, " ": 0.28, enspace: 0.5 };
const ACCENTS: Record<string, string> = { hat: "^", widehat: "^", bar: "‾", overline: "‾", vec: "→", tilde: "~", widetilde: "~", dot: "˙", ddot: "¨", acute: "´", grave: "`", breve: "˘", check: "ˇ", overrightarrow: "→" };
const FONTS: Record<string, string> = { mathbf: "bold", boldsymbol: "bold-italic", mathrm: "normal", mathit: "italic", mathbb: "double-struck", mathcal: "script", mathfrak: "fraktur", mathsf: "sans-serif", mathtt: "monospace", textbf: "bold", textit: "italic", operatorname: "normal", mathscr: "script" };
const OPEN = new Set(["(", "[", "{", "⟨", "⌈", "⌊", "|", "‖"]);

const DELIMS: Record<string, string> = { "(": "(", ")": ")", "[": "[", "]": "]", "\\{": "{", "\\}": "}", "\\langle": "⟨", "\\rangle": "⟩", "|": "|", "\\|": "‖", "\\vert": "|", "\\Vert": "‖", "\\lceil": "⌈", "\\rceil": "⌉", "\\lfloor": "⌊", "\\rfloor": "⌋", ".": "", "/": "/", "\\backslash": "\\" };

const DOUBLE_STRUCK: Record<string, string> = { R: "ℝ", N: "ℕ", Z: "ℤ", Q: "ℚ", C: "ℂ", P: "ℙ", H: "ℍ", E: "𝔼" };

class Parser {
  pos = 0;
  constructor(readonly src: string) {}

  eof(): boolean {
    return this.pos >= this.src.length;
  }

  private skipWs(): void {
    while (!this.eof() && /\s/.test(this.src[this.pos] as string)) this.pos++;
  }

  private peek(): string {
    this.skipWs();
    return this.src[this.pos] ?? "";
  }

  private commandAt(): string | null {
    this.skipWs();
    if (this.src[this.pos] !== "\\") return null;
    const m = /^\\([A-Za-z]+|.)/s.exec(this.src.slice(this.pos));
    return m ? (m[1] as string) : null;
  }

  parseRow(until: (p: Parser) => boolean): MathNode {
    const items: MathNode[] = [];
    while (!this.eof()) {
      this.skipWs();
      if (this.eof() || until(this)) break;
      const atom = this.parseAtom();
      if (atom) items.push(this.parseScripts(atom));
    }
    return items.length === 1 ? (items[0] as MathNode) : { t: "row", c: items };
  }

  atEnd(): boolean {
    this.skipWs();
    return this.src[this.pos] === "}" || this.src.startsWith("\\end", this.pos) || this.src.startsWith("\\right", this.pos) || this.src.startsWith("\\\\", this.pos) || this.src[this.pos] === "&";
  }

  private parseGroup(): MathNode {
    this.skipWs();
    if (this.src[this.pos] === "{") {
      this.pos++;
      const row = this.parseRow((p) => p.src[p.pos] === "}");
      if (this.src[this.pos] === "}") this.pos++;
      return row;
    }
    const atom = this.parseAtom();
    return atom ?? { t: "row", c: [] };
  }

  private parseOptional(): MathNode | null {
    this.skipWs();
    if (this.src[this.pos] !== "[") return null;
    const end = this.src.indexOf("]", this.pos);
    if (end < 0) return null;
    const inner = new Parser(this.src.slice(this.pos + 1, end));
    this.pos = end + 1;
    return inner.parseRow(() => false);
  }

  private rawGroup(): string {
    this.skipWs();
    if (this.src[this.pos] !== "{") {
      const c = this.src[this.pos] ?? "";
      this.pos++;
      return c;
    }
    let depth = 0;
    const start = this.pos + 1;
    for (let i = this.pos; i < this.src.length; i++) {
      const c = this.src[i];
      if (c === "\\") {
        i++;
        continue;
      }
      if (c === "{") depth++;
      else if (c === "}") {
        depth--;
        if (depth === 0) {
          const out = this.src.slice(start, i);
          this.pos = i + 1;
          return out;
        }
      }
    }
    const out = this.src.slice(start);
    this.pos = this.src.length;
    return out;
  }

  private parseScripts(base: MathNode): MathNode {
    let sub: MathNode | null = null;
    let sup: MathNode | null = null;
    for (;;) {
      this.skipWs();
      const c = this.src[this.pos];
      if (c === "^" && !sup) {
        this.pos++;
        sup = this.parseGroup();
      } else if (c === "_" && !sub) {
        this.pos++;
        sub = this.parseGroup();
      } else if (c === "'") {
        this.pos++;
        sup = sup ?? { t: "o", v: "′" };
      } else break;
    }
    if (sub && sup) return { t: "subsup", base, sub, sup };
    if (sup) return { t: "sup", base, sup };
    if (sub) return { t: "sub", base, sub };
    return base;
  }

  private parseAtom(): MathNode | null {
    this.skipWs();
    const c = this.src[this.pos];
    if (c === undefined) return null;
    if (c === "{") return this.parseGroup();
    if (c === "\\") return this.parseCommand();
    if (c === "^" || c === "_") {
      // script without a base
      return { t: "row", c: [] };
    }
    if (/\d/.test(c) || (c === "." && /\d/.test(this.src[this.pos + 1] ?? ""))) {
      const m = /^(\d+(?:\.\d+)?|\.\d+)/.exec(this.src.slice(this.pos)) as RegExpExecArray;
      this.pos += m[0].length;
      return { t: "n", v: m[0] };
    }
    if (/[A-Za-z]/.test(c)) {
      this.pos++;
      return { t: "i", v: c };
    }
    this.pos++;
    if (c === "&" || c === "}") return null;
    if (c === "~") return { t: "space", em: 0.28 };
    if (c === "%") {
      const nl = this.src.indexOf("\n", this.pos);
      this.pos = nl < 0 ? this.src.length : nl + 1;
      return null;
    }
    if (OPEN.has(c) || c === ")" || c === "]") return { t: "o", v: c, fence: true };
    if (c === ",") return { t: "o", v: ",", sep: true };
    if (c === "-") return { t: "o", v: "−" };
    if (c === "*") return { t: "o", v: "∗" };
    return { t: "o", v: c };
  }

  private parseDelimiter(): string {
    this.skipWs();
    let key = this.src[this.pos] ?? "";
    if (key === "\\") {
      const m = /^\\([A-Za-z]+|.)/s.exec(this.src.slice(this.pos)) as RegExpExecArray;
      key = m[0];
      this.pos += m[0].length;
    } else this.pos++;
    return DELIMS[key] ?? key;
  }

  private parseCommand(): MathNode | null {
    const m = /^\\([A-Za-z]+|.)/s.exec(this.src.slice(this.pos)) as RegExpExecArray;
    const name = m[1] as string;
    this.pos += m[0].length;

    if (name in GREEK) return { t: "i", v: GREEK[name] as string };
    if (name in BIG) return { t: "o", v: BIG[name] as string, large: true };
    if (name in SPACES) return { t: "space", em: SPACES[name] as number };
    if (name in ACCENTS) {
      const body = this.parseGroup();
      return { t: "over", base: body, over: { t: "o", v: ACCENTS[name] as string }, accent: true };
    }
    if (name in FONTS) {
      this.skipWs();
      const raw = FONTS[name] === "normal" && name !== "mathrm" ? this.rawGroup() : null;
      if (raw !== null) return { t: "text", v: raw };
      const body = this.parseGroup();
      if (FONTS[name] === "double-struck") return styleText(body, "double-struck");
      return { t: "style", variant: FONTS[name] as string, body };
    }
    switch (name) {
      case "frac":
      case "dfrac":
      case "tfrac": {
        const num = this.parseGroup();
        const den = this.parseGroup();
        return { t: "frac", num, den };
      }
      case "binom": {
        const num = this.parseGroup();
        const den = this.parseGroup();
        return { t: "fenced", open: "(", close: ")", body: { t: "frac", num, den, line: false } };
      }
      case "sqrt": {
        const index = this.parseOptional();
        const body = this.parseGroup();
        return { t: "sqrt", body, index: index ?? undefined };
      }
      case "text":
      case "mbox":
      case "mathrm_text":
        return { t: "text", v: this.rawGroup() };
      case "left": {
        const open = this.parseDelimiter();
        const body = this.parseRow((p) => p.src.startsWith("\\right", p.pos));
        let close = "";
        if (this.src.startsWith("\\right", this.pos)) {
          this.pos += 6;
          close = this.parseDelimiter();
        }
        return { t: "fenced", open, close, body };
      }
      case "begin": {
        const env = this.rawGroup().trim();
        return this.parseEnvironment(env);
      }
      case "end":
        this.rawGroup();
        return null;
      case "lim":
      case "max":
      case "min":
        return { t: "i", v: name, variant: "op" };
      case "overset": {
        const over = this.parseGroup();
        const base = this.parseGroup();
        return { t: "over", base, over };
      }
      case "underset": {
        const under = this.parseGroup();
        const base = this.parseGroup();
        return { t: "under", base, under };
      }
      case "underline":
        return { t: "under", base: this.parseGroup(), under: { t: "o", v: "_" } };
      case "stackrel": {
        const over = this.parseGroup();
        const base = this.parseGroup();
        return { t: "over", base, over };
      }
      case "not": {
        const next = this.parseAtom();
        if (next && next.t === "o") return { t: "o", v: `${next.v}\u0338` };
        return next;
      }
      case "displaystyle":
      case "textstyle":
      case "scriptstyle":
      case "limits":
      case "nolimits":
      case "bigl":
      case "bigr":
      case "Bigl":
      case "Bigr":
      case "big":
      case "Big":
        return null;
      case "{":
        return { t: "o", v: "{", fence: true };
      case "}":
        return { t: "o", v: "}", fence: true };
      case "|":
        return { t: "o", v: "‖" };
      case "\\":
        return null;
      case "&":
      case "%":
      case "$":
      case "#":
      case "_":
        return { t: "o", v: name };
      default:
        if (name in SYMBOLS) {
          const v = SYMBOLS[name] as string;
          return { t: "o", v, fence: ["⟨", "⟩", "⌈", "⌉", "⌊", "⌋"].includes(v) };
        }
        if (FUNCTIONS.has(name)) return { t: "i", v: name, variant: "op" };
        return { t: "error", v: `\\${name}` };
    }
  }

  private parseEnvironment(env: string): MathNode {
    const rows: MathNode[][] = [];
    let row: MathNode[] = [];
    const endTag = "\\end";
    for (;;) {
      const cell = this.parseRow((p) => p.atEnd());
      row.push(cell);
      this.skipWs();
      if (this.src.startsWith("\\\\", this.pos)) {
        this.pos += 2;
        rows.push(row);
        row = [];
        continue;
      }
      if (this.src[this.pos] === "&") {
        this.pos++;
        continue;
      }
      if (this.src.startsWith(endTag, this.pos)) {
        this.pos += endTag.length;
        this.rawGroup();
        break;
      }
      if (this.eof() || this.src[this.pos] === "}") break;
      this.pos++;
    }
    if (row.length > 1 || (row[0] && !(row[0].t === "row" && row[0].c.length === 0))) rows.push(row);
    const brackets: Record<string, [string, string]> = {
      pmatrix: ["(", ")"], bmatrix: ["[", "]"], Bmatrix: ["{", "}"], vmatrix: ["|", "|"], Vmatrix: ["‖", "‖"], matrix: ["", ""], smallmatrix: ["", ""],
    };
    if (env in brackets) {
      const [open, close] = brackets[env] as [string, string];
      const table: MathNode = { t: "table", rows };
      return open || close ? { t: "fenced", open, close, body: table } : table;
    }
    if (env === "cases") return { t: "fenced", open: "{", close: "", body: { t: "table", rows, align: "left" } };
    if (env === "array") return { t: "table", rows };
    // aligned, align, gather, split, eqnarray: columns alternate right/left
    return { t: "table", rows, align: "aligned" };
  }
}
