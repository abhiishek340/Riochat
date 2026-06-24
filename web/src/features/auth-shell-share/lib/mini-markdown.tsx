/**
 * A small, dependency-free Markdown renderer for read-only snapshots
 * (shared chats, announcements, search previews). It builds React elements
 * only - never raw HTML - and refuses URLs outside http(s)/mailto/same-origin.
 *
 * Supported: headings, paragraphs, fenced code, block quotes, bullet and
 * numbered lists, tables, rules, and inline code, bold, italic, strike-through,
 * links, images and bare http(s) links.
 */
import { Fragment, type ReactNode } from "react";
import { absolutizeApiPath } from "@/lib/api/base-url";
import { cn } from "@/lib/utils/cn";

export type Block =
  | { type: "heading"; level: number; text: string }
  | { type: "paragraph"; text: string }
  | { type: "code"; lang: string; text: string }
  | { type: "quote"; blocks: Block[] }
  | { type: "list"; ordered: boolean; items: string[] }
  | { type: "table"; head: string[]; rows: string[][] }
  | { type: "rule" };

const FENCE = /^\s{0,3}(`{3,}|~{3,})\s*([\w+#.-]*)\s*$/;
const HEADING = /^\s{0,3}(#{1,6})\s+(.*?)\s*#*\s*$/;
const RULE = /^\s{0,3}([-*_])(?:\s*\1){2,}\s*$/;
const BULLET = /^\s{0,3}[-*+]\s+(.*)$/;
const NUMBERED = /^\s{0,3}\d{1,9}[.)]\s+(.*)$/;
const QUOTE = /^\s{0,3}>\s?(.*)$/;
const TABLE_SEP = /^\s*\|?\s*:?-{2,}:?\s*(\|\s*:?-{2,}:?\s*)*\|?\s*$/;

function splitRow(line: string): string[] {
  let body = line.trim();
  if (body.startsWith("|")) body = body.slice(1);
  if (body.endsWith("|")) body = body.slice(0, -1);
  return body.split("|").map((c) => c.trim());
}

export function parseBlocks(source: string): Block[] {
  const lines = source.replace(/\r\n?/g, "\n").split("\n");
  const blocks: Block[] = [];
  let i = 0;
  while (i < lines.length) {
    const line = lines[i] ?? "";
    if (!line.trim()) {
      i++;
      continue;
    }
    const fence = FENCE.exec(line);
    if (fence) {
      const marker = fence[1] ?? "```";
      const body: string[] = [];
      i++;
      while (i < lines.length && !(lines[i] ?? "").trim().startsWith(marker)) body.push(lines[i++] ?? "");
      i++; // closing fence (an unterminated block runs to the end)
      blocks.push({ type: "code", lang: fence[2] ?? "", text: body.join("\n") });
      continue;
    }
    const heading = HEADING.exec(line);
    if (heading) {
      blocks.push({ type: "heading", level: (heading[1] ?? "#").length, text: heading[2] ?? "" });
      i++;
      continue;
    }
    if (RULE.test(line)) {
      blocks.push({ type: "rule" });
      i++;
      continue;
    }
    if (QUOTE.test(line)) {
      const inner: string[] = [];
      while (i < lines.length && QUOTE.test(lines[i] ?? "")) inner.push(QUOTE.exec(lines[i++] ?? "")?.[1] ?? "");
      blocks.push({ type: "quote", blocks: parseBlocks(inner.join("\n")) });
      continue;
    }
    const bullet = BULLET.test(line);
    if (bullet || NUMBERED.test(line)) {
      const pattern = bullet ? BULLET : NUMBERED;
      const items: string[] = [];
      while (i < lines.length && pattern.test(lines[i] ?? "")) items.push(pattern.exec(lines[i++] ?? "")?.[1] ?? "");
      blocks.push({ type: "list", ordered: !bullet, items });
      continue;
    }
    if (line.includes("|") && TABLE_SEP.test(lines[i + 1] ?? "")) {
      const head = splitRow(line);
      i += 2;
      const rows: string[][] = [];
      while (i < lines.length && (lines[i] ?? "").includes("|") && (lines[i] ?? "").trim()) rows.push(splitRow(lines[i++] ?? ""));
      blocks.push({ type: "table", head, rows });
      continue;
    }
    const para: string[] = [];
    while (i < lines.length) {
      const next = lines[i] ?? "";
      if (!next.trim() || FENCE.test(next) || HEADING.test(next) || QUOTE.test(next) || BULLET.test(next) || NUMBERED.test(next) || RULE.test(next)) break;
      para.push(next);
      i++;
    }
    if (para.length === 0) {
      para.push(line);
      i++;
    }
    blocks.push({ type: "paragraph", text: para.join("\n") });
  }
  return blocks;
}

export type Inline =
  | { type: "text"; text: string }
  | { type: "code"; text: string }
  | { type: "strong"; children: Inline[] }
  | { type: "em"; children: Inline[] }
  | { type: "del"; children: Inline[] }
  | { type: "link"; href: string; children: Inline[] }
  | { type: "image"; src: string; alt: string }
  | { type: "br" };

/** http(s), mailto and same-origin paths only; everything else (javascript:, data:, //host) is dropped. */
export function safeUrl(raw: string): string {
  const url = raw.trim();
  // Browsers ignore tabs and line breaks inside a URL, so "/<tab>/host" would become "//host".
  if (/[\u0000-\u001f\u007f]/.test(url)) return "";
  if (/^(https?:\/\/|mailto:)/i.test(url)) return url;
  if (url.startsWith("/") && !url.startsWith("//") && !url.includes("\\")) return url;
  if (url.startsWith("#")) return url;
  return "";
}

const INLINE = /(`+)([^`]|[^`][\s\S]*?[^`])\1(?!`)|!\[([^\]]*)\]\(([^()\s]*(?:\([^()\s]*\)[^()\s]*)*)(?:\s+"[^"]*")?\)|\[([^\]]+)\]\(([^()\s]*(?:\([^()\s]*\)[^()\s]*)*)(?:\s+"[^"]*")?\)|\*\*([^*]+)\*\*|__([^_]+)__|~~([^~]+)~~|\*([^*\s][^*]*)\*|(?<![\w])_([^_\s][^_]*)_(?![\w])|(https?:\/\/[^\s<>()]+[^\s<>().,;:!?'"])|\n/g;

export function parseInline(text: string): Inline[] {
  const out: Inline[] = [];
  let last = 0;
  for (const m of text.matchAll(INLINE)) {
    const at = m.index ?? 0;
    if (at > last) out.push({ type: "text", text: text.slice(last, at) });
    last = at + m[0].length;
    if (m[0] === "\n") out.push({ type: "br" });
    else if (m[2] !== undefined) out.push({ type: "code", text: m[2].trim() });
    else if (m[4] !== undefined) {
      const src = safeUrl(m[4]);
      if (src) out.push({ type: "image", src, alt: m[3] ?? "" });
      else out.push({ type: "text", text: m[3] ?? "" });
    } else if (m[6] !== undefined) {
      const href = safeUrl(m[6]);
      const children = parseInline(m[5] ?? "");
      out.push(href ? { type: "link", href, children } : { type: "text", text: m[5] ?? "" });
    } else if (m[7] !== undefined) out.push({ type: "strong", children: parseInline(m[7]) });
    else if (m[8] !== undefined) out.push({ type: "strong", children: parseInline(m[8]) });
    else if (m[9] !== undefined) out.push({ type: "del", children: parseInline(m[9]) });
    else if (m[10] !== undefined) out.push({ type: "em", children: parseInline(m[10]) });
    else if (m[11] !== undefined) out.push({ type: "em", children: parseInline(m[11]) });
    else if (m[12] !== undefined) out.push({ type: "link", href: m[12], children: [{ type: "text", text: m[12] }] });
  }
  if (last < text.length) out.push({ type: "text", text: text.slice(last) });
  return out;
}

function renderInline(nodes: Inline[]): ReactNode {
  return nodes.map((node, index) => {
    switch (node.type) {
      case "text":
        return <Fragment key={index}>{node.text}</Fragment>;
      case "br":
        return <br key={index} />;
      case "code":
        return (
          <code key={index} className="rounded bg-muted px-1 py-0.5 font-mono text-[0.9em]">
            {node.text}
          </code>
        );
      case "strong":
        return <strong key={index}>{renderInline(node.children)}</strong>;
      case "em":
        return <em key={index}>{renderInline(node.children)}</em>;
      case "del":
        return <del key={index}>{renderInline(node.children)}</del>;
      case "link":
        return (
          <a key={index} href={absolutizeApiPath(node.href)} target="_blank" rel="noopener noreferrer" className="text-primary underline underline-offset-2">
            {renderInline(node.children)}
          </a>
        );
      case "image":
        return (
          // eslint-disable-next-line @next/next/no-img-element -- snapshot images come from the share-scoped file endpoint or the web
          <img key={index} src={absolutizeApiPath(node.src)} alt={node.alt} loading="lazy" className="my-2 max-h-96 max-w-full rounded-lg border" />
        );
    }
  });
}

function renderBlocks(blocks: Block[]): ReactNode {
  return blocks.map((block, index) => {
    switch (block.type) {
      case "heading": {
        const size = ["text-xl", "text-lg", "text-base", "text-base", "text-sm", "text-sm"][block.level - 1] ?? "text-base";
        return (
          <p key={index} role="heading" aria-level={block.level} className={cn("mt-4 font-semibold first:mt-0", size)}>
            {renderInline(parseInline(block.text))}
          </p>
        );
      }
      case "paragraph":
        return <p key={index}>{renderInline(parseInline(block.text))}</p>;
      case "code":
        return (
          <pre key={index} className="overflow-x-auto rounded-lg border bg-muted/60 p-3 font-mono text-[13px] leading-relaxed">
            <code data-lang={block.lang || undefined}>{block.text}</code>
          </pre>
        );
      case "quote":
        return (
          <blockquote key={index} className="grid gap-2 border-l-2 pl-3 text-muted-foreground">
            {renderBlocks(block.blocks)}
          </blockquote>
        );
      case "list": {
        const Tag = block.ordered ? "ol" : "ul";
        return (
          <Tag key={index} className={cn("grid gap-1 pl-5", block.ordered ? "list-decimal" : "list-disc")}>
            {block.items.map((item, n) => (
              <li key={n}>{renderInline(parseInline(item))}</li>
            ))}
          </Tag>
        );
      }
      case "table":
        return (
          <div key={index} className="overflow-x-auto">
            <table className="w-full border-collapse text-sm">
              <thead>
                <tr>
                  {block.head.map((cell, n) => (
                    <th key={n} className="border-b px-3 py-1.5 text-left font-semibold">
                      {renderInline(parseInline(cell))}
                    </th>
                  ))}
                </tr>
              </thead>
              <tbody>
                {block.rows.map((row, r) => (
                  <tr key={r}>
                    {row.map((cell, n) => (
                      <td key={n} className="border-b px-3 py-1.5 align-top">
                        {renderInline(parseInline(cell))}
                      </td>
                    ))}
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        );
      case "rule":
        return <hr key={index} className="my-2 border-border" />;
    }
  });
}

export function MiniMarkdown({ source, className }: { source: string; className?: string }) {
  return <div className={cn("grid gap-3 break-words text-[15px] leading-relaxed", className)}>{renderBlocks(parseBlocks(source))}</div>;
}
