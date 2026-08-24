"use client";

import { useId, useMemo, useState, type ReactNode } from "react";
import { ChevronLeft, ChevronRight, Copy, Download } from "lucide-react";
import { Button, Input, toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { useCopy } from "../../hooks/use-copy";
import { arcPath, formatTick, linePath, niceAxis, rowsToCsv, scale } from "../../lib/blocks/chart-geometry";
import { changeStarts, diffLines, foldDiff, type DiffLine } from "../../lib/blocks/diff";
import { downloadText, timestampForFile } from "../../lib/clipboard";
import { bool, display, list, num, rec, safeImageSrc, str } from "./values";

export type Props = Record<string, unknown>;

export const CATALOG = new Set(["chart", "data-table", "card-grid", "stat-grid", "diff", "image-gallery", "quiz"]);

const PALETTE = ["var(--chart-1)", "var(--chart-2)", "var(--chart-3)", "var(--chart-4)", "var(--chart-5)"];

// --- chart -------------------------------------------------------------------------------------------------------

interface Series {
  name: string;
  data: (number | null)[];
}

function readSeries(v: unknown): Series[] {
  return list(v)
    .map((s, i) => {
      const r = rec(s);
      const data = list(r.data).map((d) => (typeof d === "number" && Number.isFinite(d) ? d : null));
      return { name: str(r.name, `#${i + 1}`), data };
    })
    .filter((s) => s.data.length > 0)
    .slice(0, 8);
}

export function ChartView({ props }: { props: Props }) {
  const t = useT();
  const kind = str(props.type, "line");
  const series = readSeries(props.series);
  const labels = list(props.x).map((x) => display(x));
  const title = str(props.title);
  if (series.length === 0) return <p className="text-sm text-muted-foreground">{t("chatBlocks.invalid", { component: "chart" })}</p>;
  const count = Math.max(...series.map((s) => s.data.length), labels.length);
  const legend = (
    <ul className="mt-2 flex flex-wrap gap-x-4 gap-y-1 text-xs text-muted-foreground">
      {(kind === "pie" ? Array.from({ length: Math.min(count, 12) }, (_, i) => labels[i] ?? `#${i + 1}`) : series.map((s) => s.name)).map((name, i) => (
        <li key={i} className="flex items-center gap-1.5">
          <span className="size-2.5 rounded-sm" style={{ background: PALETTE[i % PALETTE.length] }} />
          {name}
        </li>
      ))}
    </ul>
  );
  return (
    <figure className="min-w-0">
      {title ? <figcaption className="mb-1 text-sm font-medium">{title}</figcaption> : null}
      {kind === "pie" ? <Pie values={series[0]!.data} /> : <Plot kind={kind} series={series} labels={labels} count={count} yLabel={str(props.yLabel)} />}
      {legend}
    </figure>
  );
}

function Pie({ values }: { values: (number | null)[] }) {
  const data = values.map((v) => (v !== null && v > 0 ? v : 0)).slice(0, 12);
  const total = data.reduce((a, b) => a + b, 0);
  let angle = 0;
  return (
    <svg viewBox="0 0 200 200" className="mx-auto h-48 w-48" role="img">
      {total === 0
        ? null
        : data.map((v, i) => {
            if (v === 0) return null;
            const start = angle;
            angle += (v / total) * Math.PI * 2;
            return <path key={i} d={arcPath(100, 100, 90, start, angle)} fill={PALETTE[i % PALETTE.length]} stroke="var(--background)" strokeWidth={1.5} />;
          })}
    </svg>
  );
}

function Plot({ kind, series, labels, count, yLabel }: { kind: string; series: Series[]; labels: string[]; count: number; yLabel: string }) {
  const W = 480;
  const H = 240;
  const pad = { l: 44, r: 12, t: 10, b: 28 };
  const bar = kind === "bar";
  const y = useMemo(() => niceAxis(series.flatMap((s) => s.data.filter((d): d is number => d !== null)), 5, bar || kind === "area"), [series, bar, kind]);
  const sy = scale(y.min, y.max, H - pad.b, pad.t);
  const slot = (W - pad.l - pad.r) / Math.max(1, count);
  const px = (i: number) => (bar ? pad.l + slot * (i + 0.5) : pad.l + (count === 1 ? (W - pad.l - pad.r) / 2 : ((W - pad.l - pad.r) * i) / (count - 1)));
  const every = Math.ceil(count / 8);
  const barW = (slot * 0.72) / series.length;
  return (
    <svg viewBox={`0 0 ${W} ${H}`} className="w-full" role="img" aria-label={yLabel || undefined}>
      {y.ticks.map((tk) => (
        <g key={tk}>
          <line x1={pad.l} x2={W - pad.r} y1={sy(tk)} y2={sy(tk)} stroke="var(--border)" strokeWidth={1} />
          <text x={pad.l - 6} y={sy(tk) + 3} textAnchor="end" fontSize={10} fill="var(--muted-foreground)">
            {formatTick(tk)}
          </text>
        </g>
      ))}
      {Array.from({ length: count }, (_, i) =>
        i % every === 0 ? (
          <text key={i} x={px(i)} y={H - 10} textAnchor="middle" fontSize={10} fill="var(--muted-foreground)">
            {(labels[i] ?? String(i + 1)).slice(0, 10)}
          </text>
        ) : null,
      )}
      {series.map((s, si) => {
        const color = PALETTE[si % PALETTE.length];
        if (bar) {
          return (
            <g key={si} fill={color}>
              {s.data.map((v, i) =>
                v === null ? null : (
                  <rect key={i} x={px(i) - (barW * series.length) / 2 + barW * si} width={Math.max(1, barW - 1)} y={Math.min(sy(v), sy(0))} height={Math.abs(sy(v) - sy(0))} rx={2} />
                ),
              )}
            </g>
          );
        }
        const pts = s.data.map((v, i) => (v === null ? null : { x: px(i), y: sy(v) }));
        const d = linePath(pts);
        return (
          <g key={si}>
            {kind === "area" && d ? <path d={`${d}L${px(s.data.length - 1)} ${sy(0)}L${px(0)} ${sy(0)}Z`} fill={color} opacity={0.15} /> : null}
            <path d={d} fill="none" stroke={color} strokeWidth={2} strokeLinejoin="round" />
            {s.data.length <= 40 ? pts.map((p, i) => (p ? <circle key={i} cx={p.x} cy={p.y} r={2.5} fill={color} /> : null)) : null}
          </g>
        );
      })}
    </svg>
  );
}

// --- data table --------------------------------------------------------------------------------------------------

interface Column {
  key: string;
  label: string;
  align: "left" | "right";
}

function readColumns(props: Props, rows: Record<string, unknown>[]): Column[] {
  const cols = list(props.columns)
    .map((c) => {
      if (typeof c === "string") return { key: c, label: c, align: "left" as const };
      const r = rec(c);
      const key = str(r.key);
      return key ? { key, label: str(r.label, key), align: r.align === "right" ? ("right" as const) : ("left" as const) } : null;
    })
    .filter((c): c is Column => c !== null);
  if (cols.length > 0) return cols.slice(0, 24);
  const first = rows[0] ?? {};
  return Object.keys(first).slice(0, 24).map((key) => ({ key, label: key, align: typeof first[key] === "number" ? "right" : "left" }));
}

export function DataTableView({ props }: { props: Props }) {
  const t = useT();
  const rows = useMemo(() => list(props.rows).map(rec).slice(0, 2000), [props.rows]);
  const columns = useMemo(() => readColumns(props, rows), [props, rows]);
  const pageSize = Math.max(1, Math.min(100, Math.trunc(num(props.pageSize, 10))));
  const searchable = bool(props.searchable, rows.length > pageSize);
  const [query, setQuery] = useState("");
  const [page, setPage] = useState(0);
  const [sort, setSort] = useState<{ key: string; dir: 1 | -1 } | null>(null);
  const filtered = useMemo(() => {
    const q = query.trim().toLowerCase();
    let out = q ? rows.filter((r) => columns.some((c) => display(r[c.key]).toLowerCase().includes(q))) : rows;
    if (sort) {
      const { key, dir } = sort;
      out = [...out].sort((a, b) => {
        const x = a[key];
        const y = b[key];
        if (typeof x === "number" && typeof y === "number") return (x - y) * dir;
        return display(x).localeCompare(display(y), undefined, { numeric: true }) * dir;
      });
    }
    return out;
  }, [rows, columns, query, sort]);
  const pages = Math.max(1, Math.ceil(filtered.length / pageSize));
  const current = Math.min(page, pages - 1);
  const slice = filtered.slice(current * pageSize, (current + 1) * pageSize);
  const title = str(props.title);
  return (
    <div className="min-w-0">
      {title ? <p className="mb-1 text-sm font-medium">{title}</p> : null}
      <div className="mb-2 flex flex-wrap items-center gap-2">
        {searchable ? (
          <Input
            value={query}
            onChange={(e) => {
              setQuery(e.target.value);
              setPage(0);
            }}
            placeholder={t("chatBlocks.table.search")}
            aria-label={t("chatBlocks.table.search")}
            className="h-8 w-48 text-sm"
          />
        ) : null}
        <span className="text-xs text-muted-foreground">
          {filtered.length === rows.length ? t("chatBlocks.table.rows", { count: rows.length }) : t("chatBlocks.table.filtered", { filtered: filtered.length, total: rows.length })}
        </span>
        {bool(props.exportable, true) && rows.length > 0 ? (
          <Button
            variant="ghost"
            size="sm"
            className="ml-auto h-7 gap-1 px-2 text-xs"
            onClick={() => downloadText(`﻿${rowsToCsv(columns, filtered)}`, `table-${timestampForFile()}.csv`, "text/csv;charset=utf-8")}
          >
            <Download className="size-3.5" />
            {t("chatBlocks.table.exportCsv")}
          </Button>
        ) : null}
      </div>
      <div className="overflow-x-auto rounded-lg border">
        <table className="w-full border-collapse text-sm">
          <thead className="bg-muted/60">
            <tr>
              {columns.map((c) => (
                <th key={c.key} scope="col" className={cn("whitespace-nowrap px-3 py-1.5 font-medium", c.align === "right" ? "text-right" : "text-left")} aria-sort={sort?.key === c.key ? (sort.dir === 1 ? "ascending" : "descending") : undefined}>
                  <button type="button" className="inline-flex items-center gap-1 hover:text-primary" onClick={() => setSort((s) => (s?.key === c.key ? (s.dir === 1 ? { key: c.key, dir: -1 } : null) : { key: c.key, dir: 1 }))}>
                    {c.label}
                    <span aria-hidden className="text-[10px] text-muted-foreground">
                      {sort?.key === c.key ? (sort.dir === 1 ? "▲" : "▼") : ""}
                    </span>
                  </button>
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {slice.length === 0 ? (
              <tr>
                <td colSpan={columns.length || 1} className="px-3 py-4 text-center text-muted-foreground">
                  {t("chatBlocks.empty")}
                </td>
              </tr>
            ) : (
              slice.map((r, i) => (
                <tr key={i} className="border-t">
                  {columns.map((c) => (
                    <td key={c.key} className={cn("px-3 py-1.5 align-top", c.align === "right" && "text-right tabular-nums")}>
                      {display(r[c.key])}
                    </td>
                  ))}
                </tr>
              ))
            )}
          </tbody>
        </table>
      </div>
      {pages > 1 ? (
        <div className="mt-2 flex items-center justify-end gap-2 text-xs text-muted-foreground">
          <Button variant="ghost" size="sm" className="h-7 px-2" disabled={current === 0} onClick={() => setPage(current - 1)} aria-label={t("chatBlocks.table.prev")}>
            <ChevronLeft className="size-4" />
          </Button>
          {t("chatBlocks.table.page", { page: current + 1, total: pages })}
          <Button variant="ghost" size="sm" className="h-7 px-2" disabled={current >= pages - 1} onClick={() => setPage(current + 1)} aria-label={t("chatBlocks.table.next")}>
            <ChevronRight className="size-4" />
          </Button>
        </div>
      ) : null}
    </div>
  );
}

// --- card grid / stat grid -----------------------------------------------------------------------------------------
