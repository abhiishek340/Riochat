"use client";

import "../chat.css";
import { Fragment, memo, useMemo, useRef, useState, type ReactNode } from "react";
import { ChevronRight } from "lucide-react";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { toast } from "@/components/ui";
import { copyText } from "../../lib/clipboard";
import type { Block, Inline, TableCell } from "../../lib/markdown/ast";
import { parseBlocks } from "../../lib/markdown/blocks";
import { normalizeContent, type Variant } from "../../lib/markdown/normalize";
import { UiBlockHost, WidgetHost } from "../blocks/ui-block-host";
import { CodeBlock } from "./code-block";
import { MarkdownContext, useMarkdownContext, type MarkdownActions } from "./context";
import { MathView } from "./math-view";
import { MdImage } from "./md-image";
import { MdLink } from "./md-link";
import { MdTable } from "./md-table";

// --- thinking segments ------------------------------------------------------------------

export interface ThinkSplit {
  think: { text: string; complete: boolean } | null;
  rest: string;
}

/** Content that starts with <think> or <thinking> becomes a collapsible segment. */
export function splitThinking(content: string): ThinkSplit {
  const m = /^\s*<(think|thinking)(\s[^>]*)?>/i.exec(content);
  if (!m || /\/\s*>$/.test(m[0])) return { think: null, rest: content };
  const name = m[1] as string;
  const after = content.slice(m[0].length);
  const close = new RegExp(`</${name}\\s*>`, "i").exec(after);
  if (!close) return { think: { text: after, complete: false }, rest: "" };
  return { think: { text: after.slice(0, close.index), complete: true }, rest: after.slice(close.index + close[0].length) };
}

// --- inline rendering ---------------------------------------------------------------------

function InlineNodes({ nodes }: { nodes: readonly Inline[] }) {
  const ctx = useMarkdownContext();
  const t = useT();
  return (
    <>
      {nodes.map((n, i) => {
        switch (n.type) {
          case "text":
            return <Fragment key={i}>{n.value}</Fragment>;
          case "code":
            return <InlineCode key={i} value={n.value} />;
          case "em":
            return (
              <em key={i}>
                <InlineNodes nodes={n.children} />
              </em>
            );
          case "strong":
            return (
              <strong key={i}>
                <InlineNodes nodes={n.children} />
              </strong>
            );
          case "del":
            return (
              <del key={i}>
                <InlineNodes nodes={n.children} />
              </del>
            );
          case "link": {
            const onlyImage = n.children.length === 1 && n.children[0]?.type === "image";
            return (
              <MdLink key={i} href={n.href} title={n.title}>
                {onlyImage ? <MdImage src={(n.children[0] as Extract<Inline, { type: "image" }>).src} alt={(n.children[0] as Extract<Inline, { type: "image" }>).alt} inLink /> : <InlineNodes nodes={n.children} />}
              </MdLink>
            );
          }
          case "image":
            return <MdImage key={i} src={n.src} alt={n.alt} />;
          case "math":
            return <MathView key={i} tex={n.tex} display={false} />;
          case "br":
            return <br key={i} />;
          case "footnoteRef":
            return (
              <sup key={i} className="fn">
                <a
                  id={`${ctx.scope}-fnref-${n.id}`}
                  href={`#${ctx.scope}-fn-${n.id}`}
                  aria-label={t("chatRich.md.footnote", { index: n.index })}
                  onClick={(e) => {
                    e.preventDefault();
                    document.getElementById(`${ctx.scope}-fn-${n.id}`)?.scrollIntoView({ behavior: "smooth", block: "center" });
                  }}
                >
                  {n.index}
                </a>
              </sup>
            );
        }
      })}
    </>
  );
}

function InlineCode({ value }: { value: string }) {
  const t = useT();
  const down = useRef<{ x: number; y: number } | null>(null);
  return (
    <code
      role="button"
      tabIndex={0}
      title={t("common.actions.copy")}
      aria-label={`${t("common.actions.copy")}: ${value}`}
      onPointerDown={(e) => (down.current = { x: e.clientX, y: e.clientY })}
      onClick={async (e) => {
        if (e.metaKey || e.ctrlKey || e.shiftKey || e.altKey) return;
        const start = down.current;
        if (start && Math.hypot(e.clientX - start.x, e.clientY - start.y) > 6) return;
        const selection = window.getSelection();
        if (selection && !selection.isCollapsed) return;
        if (await copyText(value)) toast.success(t("chatRich.md.copied"));
        else toast.error(t("chatRich.md.copyFailed"));
      }}
      onKeyDown={async (e) => {
        if (e.key === "Enter" || e.key === " ") {
          e.preventDefault();
          if (await copyText(value)) toast.success(t("chatRich.md.copied"));
        }
      }}
    >
      {value}
    </code>
  );
}

// --- block rendering ------------------------------------------------------------------------

interface BlockProps {
  block: Block;
  /** Signature used to skip re-rendering finished blocks. */
  sig: string;
  tail: boolean;
  streaming: boolean;
}

function renderCell(cell: TableCell): ReactNode {
  return <InlineNodes nodes={cell.children} />;
}

function BlocksView({ blocks, streaming, tailOpen }: { blocks: readonly Block[]; streaming: boolean; tailOpen: boolean }) {
  return (
    <>
      {blocks.map((b, i) => (
        <BlockView key={`${i}:${b.type}`} block={b} sig={sigOf(b)} tail={tailOpen && i === blocks.length - 1} streaming={streaming} />
      ))}
    </>
  );
}

function sigOf(b: Block): string {
  return `${b.type}|${b.raw}|${"closed" in b ? String(b.closed) : ""}`;
}

const BlockView = memo(
  function BlockView({ block, tail, streaming }: BlockProps) {
    const ctx = useMarkdownContext();
    const line = block.line + 1;
    switch (block.type) {
      case "paragraph": {
        const kids = block.children;
        if (kids.length === 1 && (kids[0]?.type === "image" || (kids[0]?.type === "link" && kids[0].children.length === 1 && kids[0].children[0]?.type === "image"))) {
          return (
            <div data-source-line={line}>
              <InlineNodes nodes={kids} />
            </div>
          );
        }
        return (
          <p data-source-line={line}>
            <InlineNodes nodes={kids} />
          </p>
        );
      }
      case "heading": {
        const Tag = `h${block.level}` as "h1";
        return (
          <Tag id={`${ctx.scope}-${block.id}`} data-source-line={line} data-heading-level={block.level}>
            <InlineNodes nodes={block.children} />
          </Tag>
        );
      }
      case "code": {
        if (block.lang === "riochat-ui") return <UiBlockHost code={block.code} closed={block.closed && !tail} />;
        if (block.lang === "riochat-widget") return <WidgetHost code={block.code} closed={block.closed} />;
        return (
          <div data-source-line={line}>
            <CodeBlock code={block.code} lang={block.lang} closed={block.closed || !streaming} />
          </div>
        );
      }
      case "math":
        return (
          <div data-source-line={line}>
            <MathView tex={block.tex} display />
          </div>
        );
      case "quote":
        return (
          <blockquote data-source-line={line}>
            <BlocksView blocks={block.children} streaming={streaming} tailOpen={tail} />
          </blockquote>
        );
      case "list": {
        const Tag = block.ordered ? "ol" : "ul";
        const task = block.items.some((i) => i.checked !== null);
        return (
          <Tag className={cn(task && "task")} start={block.ordered && block.start !== 1 ? block.start : undefined} data-source-line={line}>
            {block.items.map((item, i) => (
              <li key={i}>
                {item.checked !== null ? <input type="checkbox" checked={item.checked} disabled readOnly aria-hidden className="mt-1.5 shrink-0" /> : null}
                <div className={task ? "min-w-0 flex-1" : undefined}>
                  {block.tight ? <TightItem blocks={item.children} streaming={streaming} tail={tail && i === block.items.length - 1} /> : <BlocksView blocks={item.children} streaming={streaming} tailOpen={tail && i === block.items.length - 1} />}
                </div>
              </li>
            ))}
          </Tag>
        );
      }
      case "table":
        return (
          <div data-source-line={line}>
            <MdTable header={block.header} align={block.align} rows={block.rows} streaming={streaming && !block.complete} renderCell={renderCell} />
          </div>
        );
      case "hr":
        return <hr data-source-line={line} />;
      case "footnotes":
        return (
          <section className="footnotes" aria-label="footnotes">
            <ol>
              {block.items.map((item) => (
                <li key={item.id} id={`${ctx.scope}-fn-${item.id}`}>
                  <TightItem blocks={item.children} streaming={false} tail={false} />{" "}
                  <a
                    href={`#${ctx.scope}-fnref-${item.id}`}
                    onClick={(e) => {
                      e.preventDefault();
                      document.getElementById(`${ctx.scope}-fnref-${item.id}`)?.scrollIntoView({ behavior: "smooth", block: "center" });
                    }}
                  >
                    ↩
                  </a>
                </li>
              ))}
            </ol>
          </section>
        );
    }
  },
  (a, b) => a.sig === b.sig && a.tail === b.tail && a.streaming === b.streaming,
);

/** A tight list item shows its first paragraph without paragraph spacing. */
function TightItem({ blocks, streaming, tail }: { blocks: readonly Block[]; streaming: boolean; tail: boolean }) {
  return (
    <>
      {blocks.map((b, i) =>
        b.type === "paragraph" ? (
          <Fragment key={i}>
            <InlineNodes nodes={b.children} />
            {i < blocks.length - 1 ? null : null}
          </Fragment>
        ) : (
          <BlockView key={`${i}:${b.type}`} block={b} sig={sigOf(b)} tail={tail && i === blocks.length - 1} streaming={streaming} />
        ),
      )}
    </>
  );
}

// --- the component --------------------------------------------------------------------------------

export interface MarkdownProps extends MarkdownActions {
  content: unknown;
  variant?: Variant;
  streaming?: boolean;
  className?: string;
  /** Prefix for heading ids; unique per message. */
  scope?: string;
  readOnly?: boolean;
  /** Open thinking segments by default while they stream. */
  autoExpandThinking?: boolean;
}

export const Markdown = memo(function Markdown({
  content,
  variant = "default",
  streaming = false,
  className,
  scope = "md",
  readOnly = false,
  autoExpandThinking = true,
  openCode,
  imageEditable,
  editImage,
  blocks,
}: MarkdownProps) {
  const text = useMemo(() => normalizeContent(content, { variant }), [content, variant]);
  const { think, rest } = useMemo(() => (variant === "thinking" ? { think: null, rest: text } : splitThinking(text)), [text, variant]);
  const parsed = useMemo(() => parseBlocks(rest, { streaming }), [rest, streaming]);
  const ctxValue = useMemo(() => ({ scope, streaming, readOnly, openCode, imageEditable, editImage, blocks }), [scope, streaming, readOnly, openCode, imageEditable, editImage, blocks]);

  return (
    <MarkdownContext.Provider value={ctxValue}>
      <div className={cn("rio-md", variant === "thinking" && "compact", variant === "user" && "user", className)}>
        {think ? <ThinkSegment text={think.text} complete={think.complete} streaming={streaming} auto={autoExpandThinking} /> : null}
        <BlocksView blocks={parsed} streaming={streaming} tailOpen={streaming} />
      </div>
    </MarkdownContext.Provider>
  );
});

function ThinkSegment({ text, complete, streaming, auto }: { text: string; complete: boolean; streaming: boolean; auto: boolean }) {
  const t = useT();
  const active = streaming && !complete;
  const [manual, setManual] = useState<boolean | null>(null);
  const open = manual ?? (active && auto);
  return (
    <div className="my-2 rounded-xl border bg-muted/40">
      <button type="button" onClick={() => setManual(!open)} aria-expanded={open} className="flex w-full items-center gap-1.5 px-3 py-2 text-left text-xs font-medium text-muted-foreground">
        <ChevronRight className={cn("size-3.5 transition-transform", open && "rotate-90")} />
        {active ? t("chatRich.md.thinking.active") : t("chatRich.md.thinking.done")}
      </button>
      {open ? (
        <div className="border-t px-3 pb-2">
          <Markdown content={text} variant="thinking" streaming={active} />
        </div>
      ) : null}
    </div>
  );
}
