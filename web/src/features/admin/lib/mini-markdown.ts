/**
 * A deliberately small Markdown reader for previews inside the console. It
 * produces a node tree (never HTML), so nothing in the text can inject markup.
 * Supported: headings, paragraphs, bullet and numbered lists, block quotes,
 * fenced code, rules, and inline code, bold, italic and http(s)/mailto links.
 */

export type Inline =
  | { t: "text"; v: string }
  | { t: "code"; v: string }
  | { t: "strong"; c: Inline[] }
  | { t: "em"; c: Inline[] }
  | { t: "link"; href: string; c: Inline[] };

export type Block =
  | { t: "heading"; level: number; c: Inline[] }
  | { t: "p"; c: Inline[] }
  | { t: "ul"; items: Inline[][] }
  | { t: "ol"; items: Inline[][] }
  | { t: "quote"; c: Inline[] }
  | { t: "code"; lang: string; v: string }
  | { t: "hr" };

const SAFE_LINK = /^(https?:\/\/|mailto:)/i;

export function parseInline(source: string): Inline[] {
  const out: Inline[] = [];
  let text = "";
  const flush = () => {
    if (text) out.push({ t: "text", v: text });
    text = "";
  };
  let i = 0;
  while (i < source.length) {
    const ch = source[i] as string;
    if (ch === "`") {
      const end = source.indexOf("`", i + 1);
      if (end > i) {
        flush();
        out.push({ t: "code", v: source.slice(i + 1, end) });
        i = end + 1;
        continue;
      }
    }
    if (ch === "*" && source[i + 1] === "*") {
      const end = source.indexOf("**", i + 2);
      if (end > i + 2) {
        flush();
        out.push({ t: "strong", c: parseInline(source.slice(i + 2, end)) });
        i = end + 2;
        continue;
      }
    }
    if ((ch === "*" || ch === "_") && source[i + 1] !== ch && source[i + 1] !== " " && source[i + 1] !== undefined) {
      const end = source.indexOf(ch, i + 1);
      if (end > i + 1 && source[end - 1] !== " ") {
        flush();
        out.push({ t: "em", c: parseInline(source.slice(i + 1, end)) });
        i = end + 1;
        continue;
      }
    }
    if (ch === "[") {
      const close = source.indexOf("](", i + 1);
      const end = close > 0 ? source.indexOf(")", close + 2) : -1;
      if (close > i && end > close) {
        const href = source.slice(close + 2, end).trim();
        if (SAFE_LINK.test(href)) {
          flush();
          out.push({ t: "link", href, c: parseInline(source.slice(i + 1, close)) });
          i = end + 1;
          continue;
        }
      }
    }
    text += ch;
    i++;
  }
  flush();
  return out;
}

export function parseBlocks(source: string): Block[] {
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  const blocks: Block[] = [];
  let para: string[] = [];
  const flushPara = () => {
    if (para.length) blocks.push({ t: "p", c: parseInline(para.join(" ")) });
    para = [];
  };
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i] as string;
    const fence = /^```\s*([\w-]*)\s*$/.exec(line.trim());
    if (fence) {
      flushPara();
      const body: string[] = [];
      i++;
      while (i < lines.length && !/^```\s*$/.test((lines[i] as string).trim())) body.push(lines[i++] as string);
      blocks.push({ t: "code", lang: fence[1] ?? "", v: body.join("\n") });
      continue;
    }
    if (!line.trim()) {
      flushPara();
      continue;
    }
    const heading = /^(#{1,6})\s+(.*)$/.exec(line);
    if (heading) {
      flushPara();
      blocks.push({ t: "heading", level: (heading[1] as string).length, c: parseInline((heading[2] as string).trim()) });
      continue;
    }
    if (/^\s*([-*_])(\s*\1){2,}\s*$/.test(line)) {
      flushPara();
      blocks.push({ t: "hr" });
      continue;
    }
    if (/^\s*[-*+]\s+/.test(line)) {
      flushPara();
      const items: Inline[][] = [];
      while (i < lines.length && /^\s*[-*+]\s+/.test(lines[i] as string)) items.push(parseInline((lines[i++] as string).replace(/^\s*[-*+]\s+/, "")));
      i--;
      blocks.push({ t: "ul", items });
      continue;
    }
    if (/^\s*\d+[.)]\s+/.test(line)) {
      flushPara();
      const items: Inline[][] = [];
      while (i < lines.length && /^\s*\d+[.)]\s+/.test(lines[i] as string)) items.push(parseInline((lines[i++] as string).replace(/^\s*\d+[.)]\s+/, "")));
      i--;
      blocks.push({ t: "ol", items });
      continue;
    }
    if (/^>\s?/.test(line)) {
      flushPara();
      const quoted: string[] = [];
      while (i < lines.length && /^>\s?/.test(lines[i] as string)) quoted.push((lines[i++] as string).replace(/^>\s?/, ""));
      i--;
      blocks.push({ t: "quote", c: parseInline(quoted.join(" ")) });
      continue;
    }
    para.push(line.trim());
  }
  flushPara();
  return blocks;
}
