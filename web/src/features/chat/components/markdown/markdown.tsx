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
