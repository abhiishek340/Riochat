"use client";

import { memo, useCallback, useEffect, useRef, useState, type RefObject } from "react";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { plainPreview } from "../lib/text";
import type { ChatMessage } from "../types";

const GUIDE_OFFSET = 48;

interface Turn {
  id: string;
  question: string;
  answer: string;
}

function turnsOf(path: readonly ChatMessage[]): Turn[] {
  const out: Turn[] = [];
  for (let i = 0; i < path.length; i++) {
    const m = path[i] as ChatMessage;
    if (m.role !== "user") continue;
    const next = path[i + 1];
    out.push({
      id: m.id,
      question: plainPreview(m.content, 240),
      answer: next && next.role === "assistant" ? plainPreview(next.content, 420) : "",
    });
  }
  return out;
}

function useRaf(callback: () => void): () => void {
  const frame = useRef(0);
  const cb = useRef(callback);
  cb.current = callback;
  useEffect(() => () => cancelAnimationFrame(frame.current), []);
  return useCallback(() => {
    cancelAnimationFrame(frame.current);
    frame.current = requestAnimationFrame(() => cb.current());
  }, []);
}

function scrollBehavior(): ScrollBehavior {
  return typeof window !== "undefined" && window.matchMedia("(prefers-reduced-motion: reduce)").matches ? "auto" : "smooth";
}

/** One tick per user turn; the current one is emphasised and hovering shows the exchange. */
export const PositionRail = memo(function PositionRail({ path, scroller }: { path: readonly ChatMessage[]; scroller: RefObject<HTMLDivElement | null> }) {
  const t = useT();
  const turns = turnsOf(path);
  const [current, setCurrent] = useState(0);
  const [hover, setHover] = useState<number | null>(null);

  const measure = useRaf(() => {
    const el = scroller.current;
    if (!el) return;
    const top = el.getBoundingClientRect().top + GUIDE_OFFSET + 40;
    let idx = 0;
    const nodes = el.querySelectorAll<HTMLElement>('[data-turn-id]');
    nodes.forEach((n, i) => {
      if (n.getBoundingClientRect().top <= top) idx = i;
    });
    setCurrent(idx);
  });

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    el.addEventListener("scroll", measure, { passive: true });
    measure();
    return () => el.removeEventListener("scroll", measure);
  }, [scroller, measure, turns.length]);

  if (turns.length < 2) return null;
  const go = (id: string) => {
    const node = scroller.current?.querySelector<HTMLElement>(`[data-turn-id="${CSS.escape(id)}"]`);
    node?.scrollIntoView({ behavior: scrollBehavior(), block: "start" });
  };
  const size = (i: number) => (i === current ? 2 : Math.abs(i - current) === 1 ? 1.5 : 1);
  return (
    <nav aria-label={t("chatMsg.rail.label")} className="pointer-events-none absolute right-2 top-1/2 z-10 hidden -translate-y-1/2 md:block" data-capture-hide>
      <ul className="pointer-events-auto flex max-h-[60dvh] flex-col items-end gap-1.5 overflow-hidden py-2">
        {turns.map((turn, i) => (
          <li key={turn.id} className="relative">
            <button
              type="button"
              onClick={() => go(turn.id)}
              onMouseEnter={() => setHover(i)}
              onMouseLeave={() => setHover((h) => (h === i ? null : h))}
              onFocus={() => setHover(i)}
              onBlur={() => setHover((h) => (h === i ? null : h))}
              aria-label={turn.question || `#${i + 1}`}
              aria-current={i === current ? "true" : undefined}
              className="flex h-3 items-center justify-end outline-none"
              style={{ width: `${size(i) * 0.6}rem` }}
            >
              <span className={cn("h-0.5 w-full rounded-full", i === current ? "bg-foreground" : "bg-muted-foreground/40")} />
            </button>
            {hover === i ? (
              <div role="tooltip" className="absolute right-full top-1/2 mr-2 w-64 -translate-y-1/2 rounded-lg border bg-popover p-2.5 text-xs shadow-lg">
                <p className="truncate font-medium">{turn.question || t("chatMsg.rail.empty")}</p>
                {turn.answer ? <p className="mt-1 line-clamp-3 text-muted-foreground">{turn.answer}</p> : null}
              </div>
            ) : null}
          </li>
        ))}
      </ul>
    </nav>
  );
});

interface Heading {
  id: string;
  level: number;
  text: string;
}

/** Headings of the reply nearest the guide line, for jumping within a long answer. */
export const OutlineRail = memo(function OutlineRail({ scroller, signature }: { scroller: RefObject<HTMLDivElement | null>; signature: string }) {
  const t = useT();
  const [headings, setHeadings] = useState<Heading[]>([]);
  const [active, setActive] = useState("");

  const measure = useRaf(() => {
    const el = scroller.current;
    if (!el) return;
    const guide = el.getBoundingClientRect().top + GUIDE_OFFSET;
    const replies = Array.from(el.querySelectorAll<HTMLElement>('[data-role="assistant"]'));
    let target: HTMLElement | null = null;
    for (const r of replies) {
      const box = r.getBoundingClientRect();
      if (box.top <= guide && box.bottom >= guide) {
        target = r;
        break;
      }
      if (box.top > guide) {
        target = target ?? r;
        break;
      }
    }
    const nodes = target ? Array.from(target.querySelectorAll<HTMLElement>("[data-heading-level]")).filter((h) => Number(h.dataset.headingLevel) <= 3 && h.id) : [];
    const next = nodes.map((h) => ({ id: h.id, level: Number(h.dataset.headingLevel), text: (h.textContent ?? "").trim() }));
    setHeadings((prev) => (prev.length === next.length && prev.every((p, i) => p.id === next[i]?.id && p.text === next[i]?.text) ? prev : next));
    let current = "";
    for (const h of nodes) if (h.getBoundingClientRect().top <= guide + 8) current = h.id;
    setActive(current);
  });

  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    el.addEventListener("scroll", measure, { passive: true });
    window.addEventListener("resize", measure);
    const mo = new MutationObserver(measure);
    mo.observe(el, { childList: true, subtree: true, characterData: true });
    measure();
    return () => {
      el.removeEventListener("scroll", measure);
      window.removeEventListener("resize", measure);
      mo.disconnect();
    };
  }, [scroller, measure, signature]);

  if (headings.length < 2) return null;
  return (
    <nav aria-label={t("chatMsg.outline.label")} className="absolute right-10 top-16 z-10 hidden max-h-[50dvh] w-48 overflow-auto rounded-xl border bg-card/90 p-2 text-xs shadow-sm backdrop-blur 2xl:block" data-capture-hide>
      <ul className="grid gap-0.5">
        {headings.map((h) => (
          <li key={h.id} style={{ paddingLeft: `${(h.level - 1) * 0.6}rem` }}>
            <button
              type="button"
              aria-label={t("chatMsg.outline.jump", { title: h.text })}
              aria-current={h.id === active ? "location" : undefined}
              onClick={() => document.getElementById(h.id)?.scrollIntoView({ behavior: scrollBehavior(), block: "start" })}
              className={cn("w-full truncate rounded px-1.5 py-1 text-left hover:bg-accent", h.id === active ? "bg-accent font-medium text-accent-foreground" : "text-muted-foreground")}
            >
              {h.text}
            </button>
          </li>
        ))}
      </ul>
    </nav>
  );
});
