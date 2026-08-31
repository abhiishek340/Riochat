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

export function NodeView({ node, ctx }: { node: ResolvedNode; ctx: RenderCtx }): ReactNode {
  const p = node.props;
  switch (node.component) {
    case "panel":
      return (
        <section className="grid gap-3 rounded-xl border bg-card p-3">
          {str(p.title) ? <h4 className="text-sm font-semibold">{str(p.title)}</h4> : null}
          <NodeList nodes={node.children} ctx={ctx} />
        </section>
      );
    case "card":
      return (
        <div className="grid gap-2 rounded-lg border p-3">
          {str(p.title) ? <p className="text-sm font-medium">{str(p.title)}</p> : null}
          <NodeList nodes={node.children} ctx={ctx} />
        </div>
      );
    case "stack":
      return (
        <div className="grid gap-2">
          <NodeList nodes={node.children} ctx={ctx} />
        </div>
      );
    case "row":
      return (
        <div className="flex flex-wrap items-end gap-3 [&>*]:min-w-[8rem] [&>*]:flex-1">
          <NodeList nodes={node.children} ctx={ctx} />
        </div>
      );
    case "grid": {
      const cols = Math.max(1, Math.min(6, Math.trunc(num(p.columns, 2))));
      return (
        <div className="grid gap-3" style={{ gridTemplateColumns: `repeat(auto-fit, minmax(min(100%, ${Math.round(720 / cols)}px), 1fr))` }}>
          <NodeList nodes={node.children} ctx={ctx} />
        </div>
      );
    }
    case "tabs":
      return <TabsNode node={node} ctx={ctx} />;
    case "collapse":
      return <Collapse node={node} ctx={ctx} />;
    case "divider":
      return <hr className="border-border" />;
    case "text": {
      const size = str(p.size);
      return (
        <p className={cn("whitespace-pre-wrap break-words", size === "small" ? "text-xs" : size === "large" ? "text-lg font-medium" : "text-sm", p.muted === true && "text-muted-foreground")}>
          {display(p.text)}
        </p>
      );
    }
    case "stat":
      return (
        <div className="rounded-lg border bg-muted/30 px-3 py-2">
          <p className="text-xs text-muted-foreground">{str(p.label)}</p>
          <p className="text-xl font-semibold tabular-nums">
            {display(p.value)}
            {str(p.unit) ? <span className="ml-0.5 text-sm font-normal text-muted-foreground">{str(p.unit)}</span> : null}
          </p>
        </div>
      );
    case "badge":
      return <span className={cn("inline-flex w-fit rounded-full border px-2 py-0.5 text-xs", TONE_CLASS[tone(p.tone)])}>{display(p.text)}</span>;
    case "progress": {
      const max = num(p.max, 100) || 100;
      const pct = Math.max(0, Math.min(100, (num(p.value, 0) / max) * 100));
      return (
        <div className="grid gap-1">
          {str(p.label) ? (
            <div className="flex justify-between text-xs text-muted-foreground">
              <span>{str(p.label)}</span>
              <span>{Math.round(pct)}%</span>
            </div>
          ) : null}
          <div className="h-2 overflow-hidden rounded-full bg-muted" role="progressbar" aria-valuenow={Math.round(pct)} aria-valuemin={0} aria-valuemax={100}>
            <div className="h-full rounded-full bg-primary transition-[width]" style={{ width: `${pct}%` }} />
          </div>
        </div>
      );
    }
    case "callout":
      return (
        <div className={cn("rounded-lg border px-3 py-2 text-sm", TONE_CLASS[tone(p.tone)])} role="note">
          {str(p.title) ? <p className="font-medium">{str(p.title)}</p> : null}
          <p className="whitespace-pre-wrap">{display(p.text)}</p>
        </div>
      );
    case "formula":
      return str(p.tex) ? <MathView tex={str(p.tex)} display /> : <code className="text-sm">{display(p.text)}</code>;
    case "slider": {
      const min = num(p.min, 0);
      const max = Math.max(min, num(p.max, 100));
      const step = num(p.step, 1) > 0 ? num(p.step, 1) : 1;
      const value = num(ControlValue(node, ctx), min);
      return (
        <Labeled label={str(p.label)} trailing={<span className="tabular-nums text-foreground">{display(value)}{str(p.unit)}</span>}>
          <input type="range" min={min} max={max} step={step} value={Math.min(max, Math.max(min, value))} disabled={!node.bind} onChange={(e) => node.bind && ctx.set(node.bind, Number(e.target.value))} className="w-full accent-[var(--primary)]" />
        </Labeled>
      );
    }
    case "select": {
      const options = readOptions(p.options);
      const cur = ControlValue(node, ctx);
      const idx = Math.max(0, options.findIndex((o) => o.value === cur));
      return (
        <Labeled label={str(p.label)}>
          <NativeSelect value={String(idx)} disabled={!node.bind} onChange={(e) => node.bind && ctx.set(node.bind, options[Number(e.target.value)]?.value ?? null)}>
            {options.map((o, i) => (
              <option key={i} value={i}>
                {o.label}
              </option>
            ))}
          </NativeSelect>
        </Labeled>
      );
    }
    case "segmented": {
      const options = readOptions(p.options);
      const cur = ControlValue(node, ctx);
      return (
        <div className="grid gap-1 text-sm">
          {str(p.label) ? <span className="text-muted-foreground">{str(p.label)}</span> : null}
          <div role="radiogroup" className="inline-flex w-fit max-w-full flex-wrap rounded-lg border bg-muted/40 p-0.5">
            {options.map((o, i) => (
              <button key={i} type="button" role="radio" aria-checked={o.value === cur} disabled={!node.bind} onClick={() => node.bind && ctx.set(node.bind, o.value)} className={cn("rounded-md px-3 py-1 text-sm", o.value === cur ? "bg-background shadow-sm" : "text-muted-foreground hover:text-foreground")}>
                {o.label}
              </button>
            ))}
          </div>
        </div>
      );
    }
    case "switch":
      return (
        <div className="flex items-center justify-between gap-3 text-sm">
          <span className="text-muted-foreground">{str(p.label)}</span>
          <Switch checked={ControlValue(node, ctx) === true} disabled={!node.bind} onCheckedChange={(v) => node.bind && ctx.set(node.bind, v)} aria-label={str(p.label)} />
        </div>
      );
    case "input": {
      const numeric = str(p.type) === "number";
      const cur = ControlValue(node, ctx);
      return (
        <Labeled label={str(p.label)}>
          <Input
            type={numeric ? "number" : "text"}
            value={cur === null || cur === undefined ? "" : String(cur)}
            placeholder={str(p.placeholder)}
            disabled={!node.bind}
            maxLength={numeric ? undefined : 500}
            onChange={(e) => {
              if (!node.bind) return;
              if (!numeric) ctx.set(node.bind, e.target.value);
              else ctx.set(node.bind, e.target.value === "" ? 0 : Number.isFinite(Number(e.target.value)) ? Number(e.target.value) : 0);
            }}
          />
        </Labeled>
      );
    }
    case "button":
      return <ActionButton node={node} ctx={ctx} />;
    default:
      if (CATALOG.has(node.component)) return <CatalogSafe node={node} />;
      return <UnknownNode node={node} />;
  }
}

function ActionButton({ node, ctx }: { node: ResolvedNode; ctx: RenderCtx }) {
  const t = useT();
  const action = buttonAction(node.props, ctx.declared);
  const needsAsk = action?.kind === "ask";
  const disabled = !action || (needsAsk && (ctx.busy || !ctx.canAsk));
  return (
    <Button
      variant={str(node.props.variant) === "primary" ? "default" : "outline"}
      size="sm"
      disabled={disabled}
      title={needsAsk ? (ctx.busy ? t("chatBlocks.ask.busy") : !ctx.canAsk ? t("chatBlocks.ask.unavailable") : undefined) : undefined}
      onClick={() => ctx.act(action)}
      className="w-fit"
    >
      {str(node.props.label, "OK")}
    </Button>
  );
}

function CatalogSafe({ node }: { node: ResolvedNode }) {
  return <div className="min-w-0">{renderCatalog(node.component, node.props)}</div>;
}

function UnknownNode({ node }: { node: ResolvedNode }) {
  const t = useT();
  return (
    <div className="rounded-lg border border-dashed p-3 text-xs text-muted-foreground">
      <p>{t("chatBlocks.unknown", { component: node.component })}</p>
      <pre className="mt-2 max-h-40 overflow-auto rounded bg-muted/50 p-2 font-mono text-[11px]">{JSON.stringify(rec(node.props), null, 2)}</pre>
    </div>
  );
}
