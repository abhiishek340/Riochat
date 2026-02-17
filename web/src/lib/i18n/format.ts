/**
 * A deliberately small MessageFormat subset:
 *   {name}                                  value substitution
 *   {n, plural, =0 {none} one {# item} other {# items}}
 *   {kind, select, a {...} other {...}}
 * `#` inside a plural branch is the formatted number. There is no escaping
 * syntax; catalogs avoid literal braces.
 */

export type FormatArgs = Record<string, string | number | boolean | null | undefined>;

interface Node {
  kind: "text" | "arg" | "plural" | "select";
  text?: string;
  name?: string;
  branches?: Record<string, Node[]>;
}

const cache = new Map<string, Node[]>();

function parse(source: string): Node[] {
  const hit = cache.get(source);
  if (hit) return hit;
  let pos = 0;

  const parseNodes = (untilBrace: boolean): Node[] => {
    const nodes: Node[] = [];
    let text = "";
    const flush = () => {
      if (text) nodes.push({ kind: "text", text });
      text = "";
    };
    while (pos < source.length) {
      const ch = source[pos] as string;
      if (ch === "}" && untilBrace) break;
      if (ch === "{") {
        flush();
        pos++;
        nodes.push(parseArgument());
        continue;
      }
      text += ch;
      pos++;
    }
    flush();
    return nodes;
  };

  const parseArgument = (): Node => {
    const start = pos;
    // the header runs up to ',' or the closing brace
    while (pos < source.length && source[pos] !== "," && source[pos] !== "}") pos++;
    const name = source.slice(start, pos).trim();
    if (source[pos] === "}") {
      pos++;
      return { kind: "arg", name };
    }
    pos++; // ','
    const typeStart = pos;
    while (pos < source.length && source[pos] !== ",") pos++;
    const type = source.slice(typeStart, pos).trim();
    pos++; // ','
    const branches: Record<string, Node[]> = {};
    for (;;) {
      while (pos < source.length && /\s/.test(source[pos] as string)) pos++;
      if (pos >= source.length) break;
      if (source[pos] === "}") {
        pos++;
        break;
      }
      const selStart = pos;
      while (pos < source.length && source[pos] !== "{" && !/\s/.test(source[pos] as string)) pos++;
      const selector = source.slice(selStart, pos);
      while (pos < source.length && source[pos] !== "{") pos++;
      pos++; // '{'
      branches[selector] = parseNodes(true);
      pos++; // '}'
    }
    return { kind: type === "plural" ? "plural" : "select", name, branches };
  };

  const nodes = parseNodes(false);
  cache.set(source, nodes);
  return nodes;
}

function render(nodes: Node[], args: FormatArgs, locale: string, hash: string | null): string {
  let out = "";
  for (const node of nodes) {
    switch (node.kind) {
      case "text":
        out += hash !== null ? (node.text as string).replace(/#/g, hash) : (node.text as string);
        break;
      case "arg": {
        const v = args[node.name as string];
        out += v === undefined || v === null ? "" : typeof v === "number" ? new Intl.NumberFormat(locale).format(v) : String(v);
        break;
      }
      case "plural": {
        const raw = args[node.name as string];
        const n = typeof raw === "number" ? raw : Number(raw ?? 0);
        const branches = node.branches as Record<string, Node[]>;
        const exact = branches[`=${n}`];
        const category = new Intl.PluralRules(locale).select(n);
        const chosen = exact ?? branches[category] ?? branches.other ?? [];
        out += render(chosen, args, locale, new Intl.NumberFormat(locale).format(n));
        break;
      }
      case "select": {
        const key = String(args[node.name as string] ?? "");
        const branches = node.branches as Record<string, Node[]>;
        out += render(branches[key] ?? branches.other ?? [], args, locale, hash);
        break;
      }
    }
  }
  return out;
}

export function formatMessage(template: string, args: FormatArgs | undefined, locale: string): string {
  if (!template.includes("{")) return template;
  return render(parse(template), args ?? {}, locale, null);
}
