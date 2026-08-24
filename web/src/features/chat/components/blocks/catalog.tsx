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

export function CardGridView({ props }: { props: Props }) {
  const t = useT();
  const items = list(props.items).map(rec).slice(0, 120);
  const tags = useMemo(() => [...new Set(items.map((i) => str(i.tag)).filter(Boolean))].slice(0, 12), [items]);
  const [tag, setTag] = useState("");
  const shown = tag ? items.filter((i) => str(i.tag) === tag) : items;
  const cols = Math.max(1, Math.min(4, Math.trunc(num(props.columns, 3))));
  if (items.length === 0) return <p className="text-sm text-muted-foreground">{t("chatBlocks.empty")}</p>;
  return (
    <div className="min-w-0">
      {tags.length > 1 ? (
        <div className="mb-2 flex flex-wrap gap-1.5" role="group">
          {["", ...tags].map((x) => (
            <button
              key={x || "all"}
              type="button"
              aria-pressed={tag === x}
              onClick={() => setTag(x)}
              className={cn("rounded-full border px-2.5 py-0.5 text-xs", tag === x ? "border-primary bg-primary text-primary-foreground" : "hover:bg-muted")}
            >
              {x || t("chatBlocks.all")}
            </button>
          ))}
        </div>
      ) : null}
      <div className="grid gap-2" style={{ gridTemplateColumns: `repeat(auto-fill, minmax(min(100%, ${Math.round(640 / cols)}px), 1fr))` }}>
        {shown.map((item, i) => (
          <div key={i} className="rounded-xl border bg-card p-3">
            {safeImageSrc(item.image) ? (
              // eslint-disable-next-line @next/next/no-img-element
              <img src={safeImageSrc(item.image)} alt={str(item.title)} referrerPolicy="no-referrer" loading="lazy" className="mb-2 h-28 w-full rounded-lg object-cover" />
            ) : null}
            <div className="flex items-start justify-between gap-2">
              <p className="text-sm font-medium">{str(item.title)}</p>
              {str(item.tag) ? <span className="shrink-0 rounded-full bg-muted px-2 py-0.5 text-[11px] text-muted-foreground">{str(item.tag)}</span> : null}
            </div>
            {str(item.text) ? <p className="mt-1 text-sm text-muted-foreground">{str(item.text)}</p> : null}
          </div>
        ))}
      </div>
      <p className="mt-1.5 text-xs text-muted-foreground">{t("chatBlocks.items", { count: shown.length })}</p>
    </div>
  );
}

export function StatGridView({ props }: { props: Props }) {
  const items = list(props.items).map(rec).slice(0, 24);
  return (
    <div className="grid grid-cols-[repeat(auto-fill,minmax(8.5rem,1fr))] gap-2">
      {items.map((s, i) => {
        const delta = str(s.delta);
        const up = delta.startsWith("+");
        const down = delta.startsWith("-") || delta.startsWith("−");
        return (
          <div key={i} className="rounded-xl border bg-card p-3">
            <p className="text-xs text-muted-foreground">{str(s.label)}</p>
            <p className="mt-0.5 text-xl font-semibold tabular-nums">
              {display(s.value)}
              {str(s.unit) ? <span className="ml-0.5 text-sm font-normal text-muted-foreground">{str(s.unit)}</span> : null}
            </p>
            {delta ? <p className={cn("text-xs", up && "text-success", down && "text-destructive", !up && !down && "text-muted-foreground")}>{delta}</p> : null}
          </div>
        );
      })}
    </div>
  );
}

// --- diff ---------------------------------------------------------------------------------------------------------

const LINE_CLASS = { same: "", add: "bg-success/15", del: "bg-destructive/15" } as const;
const MARK = { same: " ", add: "+", del: "-" } as const;

export function DiffView({ props }: { props: Props }) {
  const t = useT();
  const { copy, copied } = useCopy();
  const idBase = useId();
  const before = str(props.before);
  const after = str(props.after);
  const [split, setSplit] = useState(bool(props.split, false));
  const [expanded, setExpanded] = useState(false);
  const [cursor, setCursor] = useState(-1);
  const lines = useMemo(() => diffLines(before, after), [before, after]);
  const rows = useMemo(() => foldDiff(lines, 3, expanded), [lines, expanded]);
  const starts = useMemo(() => changeStarts(rows), [rows]);
  const hiddenTotal = rows.reduce((n, r) => n + (r.type === "fold" ? r.count : 0), 0);
  const jump = (dir: 1 | -1) => {
    if (starts.length === 0) return;
    const next = (cursor + dir + starts.length) % starts.length;
    setCursor(next);
    document.getElementById(`${idBase}-r${starts[next]}`)?.scrollIntoView({ block: "center", behavior: "smooth" });
  };
  const renderLine = (l: DiffLine, key: string | number, extra?: ReactNode) => (
    <div key={key} className={cn("flex gap-2 whitespace-pre-wrap break-words px-2", LINE_CLASS[l.kind])}>
      <span className="w-8 shrink-0 select-none text-right text-muted-foreground/70">{l.kind === "add" ? l.newNo : l.oldNo || l.newNo}</span>
      <span className="w-3 shrink-0 select-none text-muted-foreground">{MARK[l.kind]}</span>
      <span className="min-w-0 flex-1">{l.text || " "}</span>
      {extra}
    </div>
  );
  return (
    <div className="min-w-0 rounded-xl border">
      <div className="flex flex-wrap items-center gap-1 border-b bg-muted/40 px-2 py-1 text-xs">
        <span className="text-muted-foreground">
          {t("chatBlocks.diff.before")} → {t("chatBlocks.diff.after")}
        </span>
        <div className="ml-auto flex items-center gap-0.5">
          <Button variant="ghost" size="sm" className="h-6 px-2 text-xs" onClick={() => setSplit((v) => !v)}>
            {split ? t("chatBlocks.diff.unified") : t("chatBlocks.diff.split")}
          </Button>
          <Button variant="ghost" size="sm" className="h-6 px-1.5" disabled={starts.length === 0} onClick={() => jump(-1)} aria-label={t("chatBlocks.diff.prevChange")}>
            <ChevronLeft className="size-3.5 rotate-90" />
          </Button>
          <Button variant="ghost" size="sm" className="h-6 px-1.5" disabled={starts.length === 0} onClick={() => jump(1)} aria-label={t("chatBlocks.diff.nextChange")}>
            <ChevronRight className="size-3.5 rotate-90" />
          </Button>
          <Button variant="ghost" size="sm" className="h-6 gap-1 px-2 text-xs" onClick={() => void copy(after).then((ok) => !ok && toast.error(t("chatBlocks.copyFailed")))}>
            <Copy className="size-3" />
            {copied ? t("chatBlocks.copied") : t("chatBlocks.diff.copyAfter")}
          </Button>
        </div>
      </div>
      <div className="max-h-96 overflow-auto py-1 font-mono text-xs leading-5">
        {rows.map((r, i) => {
          if (r.type === "fold") {
            return (
              <button key={i} id={`${idBase}-r${i}`} type="button" onClick={() => setExpanded(true)} className="block w-full bg-muted/50 px-2 py-0.5 text-center text-[11px] text-muted-foreground hover:bg-muted">
                {t("chatBlocks.diff.hidden", { count: r.count })}
              </button>
            );
          }
          if (!split) return <div id={`${idBase}-r${i}`} key={i}>{renderLine(r.line, i)}</div>;
          const l = r.line;
          return (
            <div id={`${idBase}-r${i}`} key={i} className="grid grid-cols-2 gap-px">
              <div className={cn("whitespace-pre-wrap break-words px-2", l.kind === "del" && LINE_CLASS.del, l.kind === "add" && "opacity-0")}>{l.kind === "add" ? " " : l.text || " "}</div>
              <div className={cn("whitespace-pre-wrap break-words px-2", l.kind === "add" && LINE_CLASS.add, l.kind === "del" && "opacity-0")}>{l.kind === "del" ? " " : l.text || " "}</div>
            </div>
          );
        })}
      </div>
      {expanded && hiddenTotal === 0 && lines.some((l) => l.kind === "same") ? (
        <button type="button" className="w-full border-t px-2 py-1 text-xs text-muted-foreground hover:bg-muted" onClick={() => setExpanded(false)}>
          {t("chatBlocks.diff.collapse")}
        </button>
      ) : null}
    </div>
  );
}

// --- image gallery -----------------------------------------------------------------------------------------------------

export function GalleryView({ props }: { props: Props }) {
  const t = useT();
  const images = list(props.images)
    .map(rec)
    .map((i) => ({ src: safeImageSrc(i.src), alt: str(i.alt), caption: str(i.caption) }))
    .filter((i) => i.src)
    .slice(0, 40);
  const [at, setAt] = useState(0);
  if (images.length === 0) return <p className="text-sm text-muted-foreground">{t("chatBlocks.empty")}</p>;
  const cur = images[Math.min(at, images.length - 1)]!;
  return (
    <figure className="min-w-0">
      <div className="relative overflow-hidden rounded-xl border bg-muted/30">
        {/* eslint-disable-next-line @next/next/no-img-element */}
        <img src={cur.src} alt={cur.alt} referrerPolicy="no-referrer" className="mx-auto max-h-80 w-auto max-w-full object-contain" />
        {images.length > 1 ? (
          <>
            <Button variant="secondary" size="sm" className="absolute left-2 top-1/2 h-8 -translate-y-1/2 px-2" onClick={() => setAt((at - 1 + images.length) % images.length)} aria-label={t("chatBlocks.gallery.prev")}>
              <ChevronLeft className="size-4" />
            </Button>
            <Button variant="secondary" size="sm" className="absolute right-2 top-1/2 h-8 -translate-y-1/2 px-2" onClick={() => setAt((at + 1) % images.length)} aria-label={t("chatBlocks.gallery.next")}>
              <ChevronRight className="size-4" />
            </Button>
          </>
        ) : null}
      </div>
      <figcaption className="mt-1 flex justify-between gap-2 text-xs text-muted-foreground">
        <span>{cur.caption}</span>
        <span>{images.length > 1 ? `${Math.min(at, images.length - 1) + 1} / ${images.length}` : t("chatBlocks.gallery.count", { count: images.length })}</span>
      </figcaption>
    </figure>
  );
}

// --- quiz ------------------------------------------------------------------------------------------------------------------

export function QuizView({ props }: { props: Props }) {
  const t = useT();
  const questions = list(props.questions)
    .map(rec)
    .map((q) => ({ question: str(q.question), options: list(q.options).map((o) => str(o)).slice(0, 8), answer: Math.trunc(num(q.answer, -1)), explain: str(q.explain) }))
    .filter((q) => q.question && q.options.length > 1)
    .slice(0, 50);
  const [step, setStep] = useState(0);
  const [picked, setPicked] = useState<number | null>(null);
  const [correct, setCorrect] = useState(0);
  const [done, setDone] = useState(false);
  if (questions.length === 0) return <p className="text-sm text-muted-foreground">{t("chatBlocks.invalid", { component: "quiz" })}</p>;
  if (done) {
    return (
      <div className="rounded-xl border p-4 text-center">
        <p className="text-lg font-semibold">{t("chatBlocks.quiz.score", { correct, total: questions.length })}</p>
        <Button
          variant="outline"
          size="sm"
          className="mt-2"
          onClick={() => {
            setStep(0);
            setPicked(null);
            setCorrect(0);
            setDone(false);
          }}
        >
          {t("chatBlocks.reset")}
        </Button>
      </div>
    );
  }
  const q = questions[step]!;
  const answered = picked !== null;
  const last = step === questions.length - 1;
  return (
    <div className="rounded-xl border p-3">
      <p className="text-xs text-muted-foreground">{t("chatBlocks.quiz.step", { step: step + 1 })} / {questions.length}</p>
      <p className="mt-1 font-medium">{q.question}</p>
      <ul className="mt-2 space-y-1.5">
        {q.options.map((o, i) => {
          const isAnswer = i === q.answer;
          return (
            <li key={i}>
              <button
                type="button"
                disabled={answered}
                onClick={() => {
                  setPicked(i);
                  if (isAnswer) setCorrect((c) => c + 1);
                }}
                className={cn(
                  "w-full rounded-lg border px-3 py-1.5 text-left text-sm transition-colors",
                  !answered && "hover:bg-muted",
                  answered && isAnswer && "border-success bg-success/15",
                  answered && picked === i && !isAnswer && "border-destructive bg-destructive/10",
                )}
              >
                {o}
              </button>
            </li>
          );
        })}
      </ul>
      {answered && q.explain ? <p className="mt-2 text-sm text-muted-foreground">{q.explain}</p> : null}
      {answered ? (
        <div className="mt-2 flex justify-end">
          <Button
            size="sm"
            onClick={() => {
              if (last) setDone(true);
              else {
                setStep(step + 1);
                setPicked(null);
              }
            }}
          >
            {last ? t("chatBlocks.quiz.done") : t("chatBlocks.quiz.next")}
          </Button>
        </div>
      ) : null}
    </div>
  );
}

export function renderCatalog(component: string, props: Props): ReactNode {
  switch (component) {
    case "chart":
      return <ChartView props={props} />;
    case "data-table":
      return <DataTableView props={props} />;
    case "card-grid":
      return <CardGridView props={props} />;
    case "stat-grid":
      return <StatGridView props={props} />;
    case "diff":
      return <DiffView props={props} />;
    case "image-gallery":
      return <GalleryView props={props} />;
    case "quiz":
      return <QuizView props={props} />;
    default:
      return null;
  }
}
