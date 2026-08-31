"use client";

import { useState, type ReactNode } from "react";
import { ChevronDown } from "lucide-react";
import { Button, Input, NativeSelect, Switch } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { buttonAction, type ButtonAction, type ResolvedNode } from "../../lib/blocks/live";
import type { Value } from "../../lib/blocks/expression";
import { MathView } from "../markdown/math-view";
import { CATALOG, renderCatalog } from "./catalog";
import { bool, display, list, num, rec, str, tone, TONE_CLASS } from "./values";

export interface RenderCtx {
  values: Record<string, Value>;
  set: (name: string, value: Value) => void;
  act: (action: ButtonAction) => void;
  declared: Record<string, Value>;
  busy: boolean;
  canAsk: boolean;
}

interface Option {
  value: Value;
  label: string;
}

function readOptions(v: unknown): Option[] {
  return list(v)
    .map((o): Option | null => {
      if (typeof o === "object" && o !== null && !Array.isArray(o)) {
        const r = o as Record<string, unknown>;
        const value = (r.value ?? r.label ?? null) as Value;
        return { value, label: str(r.label, display(value)) };
      }
      if (o === null || typeof o === "object") return null;
      return { value: o as Value, label: display(o) };
    })
    .filter((o): o is Option => o !== null)
    .slice(0, 100);
}

function Labeled({ label, children, trailing }: { label: string; children: ReactNode; trailing?: ReactNode }) {
  return (
    <label className="grid gap-1 text-sm">
      <span className="flex items-center justify-between gap-2">
        <span className="text-muted-foreground">{label}</span>
        {trailing}
      </span>
      {children}
    </label>
  );
}

function ControlValue(node: ResolvedNode, ctx: RenderCtx): Value | undefined {
  return node.bind && node.bind in ctx.values ? ctx.values[node.bind] : undefined;
}

function NodeList({ nodes, ctx }: { nodes: ResolvedNode[]; ctx: RenderCtx }) {
  return (
    <>
      {nodes.map((n, i) => (
        <NodeView key={n.path || i} node={n} ctx={ctx} />
      ))}
    </>
  );
}

function Collapse({ node, ctx }: { node: ResolvedNode; ctx: RenderCtx }) {
  const [open, setOpen] = useState(bool(node.props.open, false));
  return (
    <div className="rounded-lg border">
      <button type="button" className="flex w-full items-center justify-between gap-2 px-3 py-2 text-left text-sm font-medium" aria-expanded={open} onClick={() => setOpen(!open)}>
        {str(node.props.title)}
        <ChevronDown className={cn("size-4 transition-transform", open && "rotate-180")} />
      </button>
      {open ? (
        <div className="grid gap-2 border-t p-3">
          <NodeList nodes={node.children} ctx={ctx} />
        </div>
      ) : null}
    </div>
  );
}

function TabsNode({ node, ctx }: { node: ResolvedNode; ctx: RenderCtx }) {
  const items = list(node.props.items).map((i) => str(i)).filter(Boolean).slice(0, 12);
  const [local, setLocal] = useState(items[0] ?? "");
  const bound = ControlValue(node, ctx);
  const active = node.bind ? str(bound, items[0] ?? "") : local;
  const index = Math.max(0, items.indexOf(active));
  return (
    <div>
      <div role="tablist" className="mb-2 flex flex-wrap gap-1 border-b">
        {items.map((it, i) => (
          <button
            key={it}
            type="button"
            role="tab"
            aria-selected={i === index}
            onClick={() => (node.bind ? ctx.set(node.bind, it) : setLocal(it))}
            className={cn("-mb-px border-b-2 px-3 py-1.5 text-sm", i === index ? "border-primary text-foreground" : "border-transparent text-muted-foreground hover:text-foreground")}
          >
            {it}
          </button>
        ))}
      </div>
      <div role="tabpanel" className="grid gap-2">
        {node.children[index] ? <NodeView node={node.children[index]!} ctx={ctx} /> : null}
      </div>
    </div>
  );
}
