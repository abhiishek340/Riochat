"use client";

import { useId, useState, type ReactNode } from "react";
import { abbreviateCount } from "../lib/format";

export interface BarSeries {
  key: string;
  label: string;
  color: string;
}

export interface BarDatum {
  id: string;
  /** Short axis label. */
  label: string;
  values: Record<string, number>;
}

/** Colour of the nth series: the theme's chart tokens first, then spaced hues. */
export function seriesColor(index: number): string {
  if (index < 5) return `var(--chart-${index + 1})`;
  return `oklch(0.66 0.13 ${(index * 53 + 25) % 360})`;
}

const W = 720;
const H = 260;
const M = { top: 12, right: 8, bottom: 26, left: 46 };

/** Rectangle with only the top corners rounded. */
export function topRoundedPath(x: number, y: number, w: number, h: number, r: number): string {
  const rr = Math.max(0, Math.min(r, w / 2, h));
  return `M${x},${y + h}V${y + rr}Q${x},${y} ${x + rr},${y}H${x + w - rr}Q${x + w},${y} ${x + w},${y + rr}V${y + h}Z`;
}

export function niceMax(value: number): number {
  if (!(value > 0)) return 1;
  const exp = Math.pow(10, Math.floor(Math.log10(value)));
  const f = value / exp;
  const step = f <= 1 ? 1 : f <= 2 ? 2 : f <= 5 ? 5 : 10;
  return step * exp;
}

/**
 * Stacked vertical bars. Hidden series are left out of both the stack and the
 * scale; the topmost visible segment of each bar gets the rounded corners.
 */
export function BarChart({
  data,
  series,
  hidden,
  label,
  tooltip,
  tickEvery = 1,
}: {
  data: BarDatum[];
  series: BarSeries[];
  hidden: ReadonlySet<string>;
  label: string;
  tooltip: (datum: BarDatum, index: number) => ReactNode;
  /** Show every nth x-axis label. */
  tickEvery?: number;
}) {
  const uid = useId();
  const [hover, setHover] = useState<number | null>(null);
  const visible = series.filter((s) => !hidden.has(s.key));
  const totals = data.map((d) => visible.reduce((sum, s) => sum + (d.values[s.key] ?? 0), 0));
  const max = niceMax(Math.max(0, ...totals));
  const plotW = W - M.left - M.right;
  const plotH = H - M.top - M.bottom;
  const slot = plotW / Math.max(1, data.length);
  const barW = Math.max(2, Math.min(36, slot * 0.68));
  const y = (v: number) => M.top + plotH - (v / max) * plotH;
  const ticks = [0, 1, 2, 3, 4].map((i) => (max / 4) * i);

  return (
    <div className="relative">
      <svg viewBox={`0 0 ${W} ${H}`} role="img" aria-label={label} className="h-auto w-full overflow-visible">
        {ticks.map((tick) => (
          <g key={tick}>
            <line x1={M.left} x2={W - M.right} y1={y(tick)} y2={y(tick)} stroke="var(--border)" strokeDasharray={tick === 0 ? undefined : "3 4"} />
            <text x={M.left - 6} y={y(tick) + 4} textAnchor="end" fontSize="11" fill="var(--muted-foreground)">
              {abbreviateCount(tick)}
            </text>
          </g>
        ))}
        {data.map((d, i) => {
          const cx = M.left + slot * i + slot / 2;
          let base = 0;
          const segments = visible.filter((s) => (d.values[s.key] ?? 0) > 0);
          return (
            <g key={d.id} opacity={hover === null || hover === i ? 1 : 0.55}>
              {segments.map((s, j) => {
                const v = d.values[s.key] ?? 0;
                const top = y(base + v);
                const height = y(base) - top;
                base += v;
                const isTop = j === segments.length - 1;
                return isTop ? (
                  <path key={s.key} d={topRoundedPath(cx - barW / 2, top, barW, height, 3)} fill={s.color} />
                ) : (
                  <rect key={s.key} x={cx - barW / 2} y={top} width={barW} height={height} fill={s.color} />
                );
              })}
              {i % tickEvery === 0 ? (
                <text x={cx} y={H - 8} textAnchor="middle" fontSize="11" fill="var(--muted-foreground)">
                  {d.label}
                </text>
              ) : null}
              <rect
                data-testid={`${uid}-hit-${i}`}
                x={M.left + slot * i}
                y={M.top}
                width={slot}
                height={plotH}
                fill="transparent"
                onMouseEnter={() => setHover(i)}
                onMouseLeave={() => setHover((h) => (h === i ? null : h))}
                onFocus={() => setHover(i)}
                onBlur={() => setHover((h) => (h === i ? null : h))}
                tabIndex={0}
                role="img"
                aria-label={d.label}
                aria-describedby={hover === i ? `${uid}-tip` : undefined}
              />
            </g>
          );
        })}
      </svg>
      {hover !== null && data[hover] ? (
        <div
          id={`${uid}-tip`}
          className="pointer-events-none absolute top-2 z-10 w-56 rounded-lg border bg-popover p-3 text-xs text-popover-foreground shadow-md"
          style={{ left: `clamp(0%, calc(${((M.left + slot * hover + slot / 2) / W) * 100}% - 7rem), calc(100% - 14rem))` }}
        >
          {tooltip(data[hover], hover)}
        </div>
      ) : null}
    </div>
  );
}

export function Legend({
  series,
  hidden,
  onToggle,
}: {
  series: BarSeries[];
  hidden: ReadonlySet<string>;
  onToggle: (key: string) => void;
}) {
  return (
    <ul className="flex flex-wrap gap-x-4 gap-y-1.5">
      {series.map((s) => {
        const off = hidden.has(s.key);
        return (
          <li key={s.key}>
            <button
              type="button"
              aria-pressed={!off}
              onClick={() => onToggle(s.key)}
              className="inline-flex items-center gap-1.5 rounded-sm text-xs outline-none focus-visible:ring-2 focus-visible:ring-ring"
            >
              <span className="size-2.5 rounded-[3px]" style={{ background: off ? "var(--border)" : s.color }} />
              <span className={off ? "text-muted-foreground line-through" : ""}>{s.label}</span>
            </button>
          </li>
        );
      })}
    </ul>
  );
}
