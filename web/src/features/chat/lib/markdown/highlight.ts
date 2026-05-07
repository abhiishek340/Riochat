/**
 * A compact syntax highlighter: tokenizes with ordered rules per language
 * family. It is deliberately forgiving; anything it does not understand stays
 * plain text.
 */

export type TokenType = "plain" | "keyword" | "string" | "number" | "comment" | "function" | "type" | "operator" | "punct" | "attr" | "tag" | "literal" | "variable" | "inserted" | "deleted" | "meta";

export interface Token {
  type: TokenType;
  text: string;
}

interface Rule {
  type: TokenType;
  re: RegExp;
}

const kw = (words: string): RegExp => new RegExp(`^(?:${words.split(/\s+/).join("|")})\\b`);
const kwi = (words: string): RegExp => new RegExp(`^(?:${words.split(/\s+/).join("|")})\\b`, "i");

const NUM = /^(?:0[xX][0-9a-fA-F_]+|0[bB][01_]+|\d[\d_]*(?:\.\d[\d_]*)?(?:[eE][+-]?\d+)?[fFlLuU]*)/;
const IDENT_CALL = /^[A-Za-z_$][\w$]*(?=\s*\()/;
const IDENT = /^[A-Za-z_$][\w$]*/;
const CAP_TYPE = /^[A-Z][\w$]*/;
const OPS = /^(?:=>|->|::|\+\+|--|&&|\|\||[=!<>]=?=?|[-+*/%&|^~?:]=?|\.\.\.?)/;
const PUNCT = /^[{}()[\];,.]/;

const cStr: Rule[] = [
  { type: "string", re: /^"(?:\\.|[^"\\\n])*"?/ },
  { type: "string", re: /^'(?:\\.|[^'\\\n])*'?/ },
];

function family(words: string, extra: Rule[] = [], lineComment = "//", block = true, caseInsensitive = false): Rule[] {
  const comment = lineComment === "#" ? /^#[^\n]*/ : lineComment === "--" ? /^--[^\n]*/ : /^\/\/[^\n]*/;
  return [
    ...(block ? [{ type: "comment" as const, re: /^\/\*[\s\S]*?(?:\*\/|$)/ }] : []),
    { type: "comment", re: comment },
    ...extra,
    ...cStr,
    { type: "number", re: NUM },
    { type: "keyword", re: caseInsensitive ? kwi(words) : kw(words) },
    { type: "literal", re: kw("true false null nil None True False undefined NaN Infinity") },
    { type: "function", re: IDENT_CALL },
    { type: "type", re: CAP_TYPE },
    { type: "plain", re: IDENT },
    { type: "operator", re: OPS },
    { type: "punct", re: PUNCT },
  ];
}

const JS_KW =
  "abstract as async await break case catch class const continue debugger declare default delete do else enum export extends finally for from function get if implements import in instanceof interface is keyof let namespace new of override private protected public readonly return satisfies set static super switch this throw try type typeof var void while with yield";
const GO_KW = "break case chan const continue default defer else fallthrough for func go goto if import interface map package range return select struct switch type var";
const RUST_KW = "as async await break const continue crate dyn else enum extern fn for if impl in let loop match mod move mut pub ref return self Self static struct super trait type unsafe use where while";
const JAVA_KW =
  "abstract assert boolean break byte case catch char class const continue default do double else enum extends final finally float for goto if implements import instanceof int interface long native new package private protected public return short static strictfp super switch synchronized this throw throws transient try void volatile while var record sealed fun val when object companion data override open internal lateinit suspend";
const C_KW =
  "auto break case char const continue default do double else enum extern float for goto if inline int long register restrict return short signed sizeof static struct switch typedef union unsigned void volatile while class namespace template typename public private protected virtual using new delete operator this nullptr constexpr noexcept override final bool string";
const CS_KW = "abstract as base bool break byte case catch char checked class const continue decimal default delegate do double else enum event explicit extern finally fixed float for foreach goto if implicit in int interface internal is lock long namespace new object operator out override params private protected public readonly ref return sbyte sealed short sizeof static string struct switch this throw try typeof uint ulong unchecked unsafe ushort using var virtual void volatile while async await record";
const PY_KW = "and as assert async await break class continue def del elif else except finally for from global if import in is lambda nonlocal not or pass raise return try while with yield match case self cls";
const RUBY_KW = "alias and begin break case class def defined do else elsif end ensure false for if in module next nil not or redo rescue retry return self super then true undef unless until when while yield";
const SH_KW = "if then else elif fi for while until do done case esac in function select time export local readonly declare unset return exit break continue source alias echo cd set trap";
const SQL_KW =
  "select from where group by order having limit offset insert into values update set delete create alter drop table index view database schema join inner outer left right full cross on as and or not null is in exists between like distinct union all case when then else end primary key foreign references default unique check constraint with returning begin commit rollback grant revoke asc desc count sum avg min max int integer varchar text boolean date timestamp";
const PHP_KW = "abstract and array as break callable case catch class clone const continue declare default do echo else elseif empty enddeclare endfor endforeach endif endswitch endwhile extends final finally fn for foreach function global goto if implements include include_once instanceof insteadof interface isset list match namespace new or print private protected public require require_once return static switch throw trait try unset use var while yield";
const SWIFT_KW = "actor associatedtype async await break case catch class continue default defer deinit do else enum extension fallthrough fileprivate for func guard if import in init inout internal is let nil open operator private protocol public repeat rethrows return self Self static struct subscript super switch throws try typealias var where while";
const DART_KW = "abstract as assert async await break case catch class const continue covariant default deferred do dynamic else enum export extends extension external factory final finally for Function get hide if implements import in interface is late library mixin new on operator part required rethrow return set show static super switch sync this throw try typedef var void while with yield";

const LANGS: Record<string, Rule[]> = {};

/** Rules are written with leading anchors; matching uses sticky expressions so no text is copied per token. */
function sticky(rules: Rule[]): Rule[] {
  return rules.map((r) => ({ type: r.type, re: new RegExp(r.re.source.replace(/(^|\|)\^/g, "$1"), r.re.flags.replace("y", "") + "y") }));
}

function alias(names: string[], rules: Rule[]): void {
  const prepared = sticky(rules);
  for (const n of names) LANGS[n] = prepared;
}

const jsRules = family(JS_KW, [{ type: "string", re: /^`(?:\\[\s\S]|[^`\\])*`?/ }, { type: "meta", re: /^@[A-Za-z_]\w*/ }]);
alias(["js", "javascript", "mjs", "cjs", "jsx", "ts", "typescript", "tsx"], jsRules);
alias(["go", "golang"], family(GO_KW, [{ type: "string", re: /^`[^`]*`?/ }]));
alias(["rs", "rust"], family(RUST_KW, [{ type: "meta", re: /^#!?\[[^\]]*\]/ }, { type: "variable", re: /^'[a-z_]+\b/ }]));
alias(["java", "kotlin", "kt", "scala"], family(JAVA_KW, [{ type: "meta", re: /^@[A-Za-z_]\w*/ }]));
alias(["c", "h", "cpp", "cc", "hpp", "c++", "cxx", "objc"], family(C_KW, [{ type: "meta", re: /^#\s*\w+[^\n]*/ }]));
alias(["cs", "csharp", "c#"], family(CS_KW, [{ type: "meta", re: /^#\s*\w+[^\n]*/ }]));
alias(["php"], family(PHP_KW, [{ type: "variable", re: /^\$\w+/ }]));
alias(["swift"], family(SWIFT_KW, [{ type: "meta", re: /^@[A-Za-z_]\w*/ }]));
alias(["dart"], family(DART_KW));
alias(["py", "python", "python3"], [
  { type: "string", re: /^(?:[rRbBfFuU]{0,2})(?:"""[\s\S]*?(?:"""|$)|'''[\s\S]*?(?:'''|$))/ },
  { type: "comment", re: /^#[^\n]*/ },
  { type: "string", re: /^(?:[rRbBfFuU]{0,2})"(?:\\.|[^"\\\n])*"?/ },
  { type: "string", re: /^(?:[rRbBfFuU]{0,2})'(?:\\.|[^'\\\n])*'?/ },
  { type: "meta", re: /^@[A-Za-z_][\w.]*/ },
  { type: "number", re: NUM },
  { type: "keyword", re: kw(PY_KW) },
  { type: "literal", re: kw("True False None") },
  { type: "function", re: IDENT_CALL },
  { type: "type", re: CAP_TYPE },
  { type: "plain", re: IDENT },
  { type: "operator", re: OPS },
  { type: "punct", re: PUNCT },
]);
alias(["rb", "ruby"], family(RUBY_KW, [{ type: "variable", re: /^[@$]{1,2}\w+/ }, { type: "variable", re: /^:\w+/ }], "#", false));
alias(["sh", "bash", "zsh", "shell", "console", "fish"], [
  { type: "comment", re: /^#[^\n]*/ },
  { type: "string", re: /^"(?:\\.|[^"\\])*"?/ },
  { type: "string", re: /^'[^']*'?/ },
  { type: "variable", re: /^\$(?:\{[^}]*\}?|\w+|[@#?*$!-])/ },
  { type: "keyword", re: kw(SH_KW) },
  { type: "number", re: NUM },
  { type: "attr", re: /^--?[A-Za-z][\w-]*/ },
  { type: "function", re: /^[\w./-]+(?=\s)/ },
  { type: "operator", re: /^(?:&&|\|\||[|&;<>]+|=)/ },
  { type: "plain", re: /^[^\s]+/ },
]);
alias(["sql", "mysql", "postgres", "postgresql", "sqlite", "plsql"], family(SQL_KW, [], "--", true, true));
alias(["yaml", "yml", "toml", "ini", "conf", "dockerfile", "makefile", "properties"], [
  { type: "comment", re: /^#[^\n]*/ },
  { type: "string", re: /^"(?:\\.|[^"\\\n])*"?/ },
  { type: "string", re: /^'[^'\n]*'?/ },
  { type: "attr", re: /^[\w.-]+(?=\s*[:=])/ },
  { type: "literal", re: kwi("true false null yes no on off") },
  { type: "number", re: NUM },
  { type: "meta", re: /^[&*]\w+/ },
  { type: "punct", re: /^[:\-[\]{},=|>]/ },
  { type: "plain", re: /^[^\s:,[\]{}]+/ },
]);
alias(["json", "jsonc", "json5"], [
  { type: "comment", re: /^\/\/[^\n]*/ },
  { type: "attr", re: /^"(?:\\.|[^"\\\n])*"(?=\s*:)/ },
  { type: "string", re: /^"(?:\\.|[^"\\\n])*"?/ },
  { type: "number", re: /^-?\d+(?:\.\d+)?(?:[eE][+-]?\d+)?/ },
  { type: "literal", re: kw("true false null") },
  { type: "punct", re: /^[{}[\],:]/ },
]);
alias(["html", "htm", "xml", "svg", "xhtml", "vue"], [
  { type: "comment", re: /^<!--[\s\S]*?(?:-->|$)/ },
  { type: "meta", re: /^<![A-Za-z][^>]*>?/ },
  { type: "tag", re: /^<\/?[A-Za-z][\w:-]*/ },
  { type: "tag", re: /^\/?>/ },
  { type: "attr", re: /^[\w:@.-]+(?=\s*=)/ },
  { type: "string", re: /^"[^"]*"?/ },
  { type: "string", re: /^'[^']*'?/ },
  { type: "operator", re: /^=/ },
  { type: "plain", re: /^[^<>=\s"']+|^\s+/ },
]);
alias(["css", "scss", "sass", "less"], [
  { type: "comment", re: /^\/\*[\s\S]*?(?:\*\/|$)/ },
  { type: "comment", re: /^\/\/[^\n]*/ },
  { type: "string", re: /^"(?:\\.|[^"\\\n])*"?/ },
  { type: "string", re: /^'(?:\\.|[^'\\\n])*'?/ },
  { type: "meta", re: /^@[\w-]+/ },
  { type: "number", re: /^#[0-9a-fA-F]{3,8}\b|^-?\d*\.?\d+(?:px|em|rem|%|vh|vw|s|ms|deg|fr)?/ },
  { type: "attr", re: /^[\w-]+(?=\s*:)/ },
  { type: "function", re: /^[\w-]+(?=\()/ },
  { type: "tag", re: /^[.#]?[A-Za-z_][\w-]*/ },
  { type: "punct", re: /^[{}();:,]/ },
  { type: "operator", re: /^[>+~*=]/ },
]);

export function normalizeLang(lang: string): string {
  return lang.trim().toLowerCase().replace(/^language-/, "");
}

export function isHighlightable(lang: string): boolean {
  const l = normalizeLang(lang);
  return l in LANGS || l === "diff" || l === "patch";
}

function highlightDiff(code: string): Token[] {
  const out: Token[] = [];
  for (const line of code.split("\n")) {
    const type: TokenType = line.startsWith("+") && !line.startsWith("+++") ? "inserted" : line.startsWith("-") && !line.startsWith("---") ? "deleted" : line.startsWith("@@") ? "meta" : "plain";
    out.push({ type, text: line + "\n" });
  }
  if (out.length) {
    const last = out[out.length - 1] as Token;
    last.text = last.text.slice(0, -1);
  }
  return out;
}

const MAX_HIGHLIGHT = 60_000;

export function highlight(code: string, lang: string): Token[] {
  const l = normalizeLang(lang);
  if (l === "diff" || l === "patch") return highlightDiff(code);
  const rules = LANGS[l];
  if (!rules || code.length > MAX_HIGHLIGHT) return [{ type: "plain", text: code }];
  const tokens: Token[] = [];
  let plain = "";
  const push = (type: TokenType, text: string) => {
    if (type === "plain") {
      plain += text;
      return;
    }
    if (plain) {
      tokens.push({ type: "plain", text: plain });
      plain = "";
    }
    tokens.push({ type, text });
  };
  let pos = 0;
  while (pos < code.length) {
    let matched = false;
    for (const rule of rules) {
      rule.re.lastIndex = pos;
      const m = rule.re.exec(code);
      if (m && m[0].length > 0) {
        push(rule.type, m[0]);
        pos += m[0].length;
        matched = true;
        break;
      }
    }
    if (!matched) {
      plain += code[pos];
      pos++;
    }
  }
  if (plain) tokens.push({ type: "plain", text: plain });
  return tokens;
}
