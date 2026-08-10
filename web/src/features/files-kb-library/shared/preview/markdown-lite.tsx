import { Fragment, type ReactNode } from "react";
import { isSafeHref, parseBlocks } from "./markdown-parse";

/**
 * A small, dependency-free Markdown reader for file previews. It understands
 * headings, fenced code, quotes, flat lists, rules, emphasis, inline code and
 * http(s)/mailto links, and never injects HTML.
 */

const INLINE = /(`[^`]+`)|(\*\*[^*]+\*\*)|(\*[^*\s][^*]*\*)|(\[[^\]]+\]\([^)\s]+\))/g;

function inline(text: string): ReactNode[] {
  const out: ReactNode[] = [];
  let last = 0;
  let key = 0;
  for (const m of text.matchAll(INLINE)) {
    const at = m.index ?? 0;
    if (at > last) out.push(text.slice(last, at));
    const token = m[0];
    if (m[1]) {
      out.push(
        <code key={key++} className="rounded bg-muted px-1 py-0.5 font-mono text-[0.9em]">
          {token.slice(1, -1)}
        </code>,
      );
    } else if (m[2]) {
      out.push(<strong key={key++}>{token.slice(2, -2)}</strong>);
    } else if (m[3]) {
      out.push(<em key={key++}>{token.slice(1, -1)}</em>);
    } else {
      const link = /^\[([^\]]+)\]\(([^)\s]+)\)$/.exec(token);
      if (link && isSafeHref(link[2] as string)) {
        out.push(
          <a key={key++} href={link[2]} target="_blank" rel="noopener noreferrer nofollow" className="text-primary underline underline-offset-2">
            {link[1]}
          </a>,
        );
      } else out.push(token);
    }
    last = at + token.length;
  }
  if (last < text.length) out.push(text.slice(last));
  return out;
}

const HEADING_CLASS = ["text-2xl", "text-xl", "text-lg", "text-base", "text-sm", "text-sm"];

export function MarkdownLite({ source }: { source: string }) {
  const blocks = parseBlocks(source);
  return (
    <div className="grid gap-3 text-sm leading-relaxed">
      {blocks.map((b, index) => {
        switch (b.type) {
          case "heading":
            return (
              <p key={index} role="heading" aria-level={b.level} className={`${HEADING_CLASS[b.level - 1]} mt-2 font-semibold tracking-tight`}>
                {inline(b.text)}
              </p>
            );
          case "code":
            return (
              <pre key={index} className="overflow-x-auto rounded-lg bg-muted p-3 font-mono text-[13px] leading-relaxed">
                {b.text}
              </pre>
            );
          case "quote":
            return (
              <blockquote key={index} className="border-l-2 border-border pl-3 text-muted-foreground">
                {inline(b.text)}
              </blockquote>
            );
          case "list": {
            const Tag = b.ordered ? "ol" : "ul";
            return (
              <Tag key={index} className={`${b.ordered ? "list-decimal" : "list-disc"} grid gap-1 pl-5`}>
                {b.items.map((item, n) => (
                  <li key={n}>{inline(item)}</li>
                ))}
              </Tag>
            );
          }
          case "rule":
            return <hr key={index} className="border-border" />;
          default:
            return (
              <p key={index} className="whitespace-pre-wrap">
                {inline(b.text).map((node, n) => (
                  <Fragment key={n}>{node}</Fragment>
                ))}
              </p>
            );
        }
      })}
    </div>
  );
}
