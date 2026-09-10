import { Fragment, type ReactNode } from "react";
import { cn } from "@/lib/utils/cn";
import { parseBlocks, type Inline } from "../lib/mini-markdown";

function renderInline(nodes: readonly Inline[]): ReactNode {
  return nodes.map((node, i) => {
    switch (node.t) {
      case "text":
        return <Fragment key={i}>{node.v}</Fragment>;
      case "code":
        return (
          <code key={i} className="rounded bg-muted px-1 py-0.5 font-mono text-[0.9em]">
            {node.v}
          </code>
        );
      case "strong":
        return <strong key={i}>{renderInline(node.c)}</strong>;
      case "em":
        return <em key={i}>{renderInline(node.c)}</em>;
      case "link":
        return (
          <a key={i} href={node.href} target="_blank" rel="noopener noreferrer nofollow" className="text-primary underline underline-offset-2">
            {renderInline(node.c)}
          </a>
        );
    }
  });
}

/** Renders the small Markdown subset the announcement editor previews. */
export function MarkdownPreview({ source, className }: { source: string; className?: string }) {
  const blocks = parseBlocks(source);
  return (
    <div className={cn("space-y-3 text-sm leading-relaxed", className)}>
      {blocks.map((block, i) => {
        switch (block.t) {
          case "heading": {
            const size = ["text-xl", "text-lg", "text-base", "text-sm", "text-sm", "text-sm"][block.level - 1] ?? "text-sm";
            return (
              <p key={i} role="heading" aria-level={block.level} className={cn("font-semibold", size)}>
                {renderInline(block.c)}
              </p>
            );
          }
          case "p":
            return <p key={i}>{renderInline(block.c)}</p>;
          case "ul":
            return (
              <ul key={i} className="list-disc space-y-1 pl-5">
                {block.items.map((item, j) => (
                  <li key={j}>{renderInline(item)}</li>
                ))}
              </ul>
            );
          case "ol":
            return (
              <ol key={i} className="list-decimal space-y-1 pl-5">
                {block.items.map((item, j) => (
                  <li key={j}>{renderInline(item)}</li>
                ))}
              </ol>
            );
          case "quote":
            return (
              <blockquote key={i} className="border-l-2 pl-3 text-muted-foreground">
                {renderInline(block.c)}
              </blockquote>
            );
          case "code":
            return (
              <pre key={i} className="overflow-auto rounded-lg border bg-muted/50 p-3 font-mono text-[12.5px]">
                <code>{block.v}</code>
              </pre>
            );
          case "hr":
            return <hr key={i} />;
        }
      })}
    </div>
  );
}

