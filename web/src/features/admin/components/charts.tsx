"use client";

import { useId, useState, type ReactNode } from "react";
import { niceAxis } from "../statistics/rules";

export interface SeriesPoint {
  label: string;
  value: number;
}

const W = 640;
const H = 220;
const M = { top: 12, right: 12, bottom: 28, left: 52 };

/** A single-series line with an area underneath. Points can be inspected by hover or focus. */
export function LineChart({ points, format, label, empty }: { points: readonly SeriesPoint[]; format: (value: number) => string; label: string; empty?: ReactNode }) {
  const [active, setActive] = useState<number | null>(null);
  const gradient = useId();
  const axis = niceAxis(Math.max(0, ...points.map((p) => p.value)));
  const innerW = W - M.left - M.right;
  const innerH = H - M.top - M.bottom;
  const x = (i: number) => M.left + (points.length <= 1 ? innerW / 2 : (i / (points.length - 1)) * innerW);
  const y = (v: number) => M.top + innerH - (v / axis.max) * innerH;
  const line = points.map((p, i) => `${i === 0 ? "M" : "L"}${x(i).toFixed(1)},${y(p.value).toFixed(1)}`).join(" ");
  const area = points.length > 0 ? `${line} L${x(points.length - 1).toFixed(1)},${(M.top + innerH).toFixed(1)} L${x(0).toFixed(1)},${(M.top + innerH).toFixed(1)} Z` : "";
  const labelEvery = Math.max(1, Math.ceil(points.length / 7));
  const shown = active !== null ? points[active] : undefined;

  if (points.length === 0 && empty) return <p className="py-10 text-center text-sm text-muted-foreground">{empty}</p>;
  return (
    <div className="relative">
      <svg viewBox={`0 0 ${W} ${H}`} role="group" aria-label={label} className="h-auto w-full" onMouseLeave={() => setActive(null)}>
        <defs>
          <linearGradient id={gradient} x1="0" y1="0" x2="0" y2="1">
            <stop offset="0%" stopColor="var(--chart-1)" stopOpacity="0.28" />
            <stop offset="100%" stopColor="var(--chart-1)" stopOpacity="0" />
          </linearGradient>
        </defs>
        {axis.ticks.map((tick) => (
          <g key={tick}>
            <line x1={M.left} x2={W - M.right} y1={y(tick)} y2={y(tick)} stroke="var(--border)" strokeDasharray={tick === 0 ? undefined : "3 4"} />
            <text x={M.left - 8} y={y(tick) + 4} textAnchor="end" className="fill-muted-foreground text-[10px] tabular-nums">
              {format(tick)}
            </text>
          </g>
        ))}
        {area ? <path d={area} fill={`url(#${gradient})`} /> : null}
        {line ? <path d={line} fill="none" stroke="var(--chart-1)" strokeWidth={2} strokeLinejoin="round" strokeLinecap="round" /> : null}
        {points.map((p, i) => (
          <g key={i}>
            {i % labelEvery === 0 ? (
              <text x={x(i)} y={H - 8} textAnchor="middle" className="fill-muted-foreground text-[10px]">
                {p.label}
              </text>
            ) : null}
            <circle
              cx={x(i)}
              cy={y(p.value)}
              r={active === i ? 4.5 : 3}
              fill="var(--background)"
              stroke="var(--chart-1)"
              strokeWidth={2}
              tabIndex={0}
              role="img"
              aria-label={`${p.label}: ${format(p.value)}`}
              onMouseEnter={() => setActive(i)}
              onFocus={() => setActive(i)}
              onBlur={() => setActive(null)}
              className="outline-none focus-visible:stroke-[3]"
            />
          </g>
        ))}
      </svg>
      {shown && active !== null ? (
        <div className="pointer-events-none absolute top-1 rounded-md bg-foreground px-2 py-1 text-xs text-background shadow-md" style={{ left: `${(x(active) / W) * 100}%`, transform: "translateX(-50%)" }}>
          <div className="font-medium tabular-nums">{format(shown.value)}</div>
          <div className="opacity-80">{shown.label}</div>
        </div>
      ) : null}
    </div>
  );
}

export interface BarItem {
  key: string | number;
  label: ReactNode;
  value: number;
  hint?: ReactNode;
}

/** Horizontal bars for a ranking; the longest bar is the largest value. */
export function BarList({ items, format, empty }: { items: readonly BarItem[]; format: (value: number) => string; empty: ReactNode }) {
  const max = Math.max(0, ...items.map((i) => i.value));
  if (items.length === 0) return <p className="py-8 text-center text-sm text-muted-foreground">{empty}</p>;
  return (
    <ol className="space-y-2.5">
      {items.map((item, index) => (
        <li key={item.key} className="grid gap-1">
          <div className="flex items-baseline justify-between gap-3 text-sm">
            <span className="flex min-w-0 items-center gap-2">
              <span className="w-4 shrink-0 text-right text-xs tabular-nums text-muted-foreground">{index + 1}</span>
              <span className="min-w-0 truncate font-medium">{item.label}</span>
            </span>
            <span className="shrink-0 tabular-nums">{format(item.value)}</span>
          </div>
          <div className="h-1.5 overflow-hidden rounded-full bg-muted" aria-hidden>
            <div className="h-full rounded-full bg-[var(--chart-1)]" style={{ width: `${max > 0 ? Math.max(2, (item.value / max) * 100) : 0}%` }} />
          </div>
          {item.hint ? <div className="pl-6 text-xs text-muted-foreground">{item.hint}</div> : null}
        </li>
      ))}
    </ol>
  );
}
