/** Scales and ticks for the SVG charts. */

export function niceStep(range: number, target: number): number {
  if (!(range > 0) || !Number.isFinite(range)) return 1;
  const raw = range / Math.max(1, target);
  const mag = Math.pow(10, Math.floor(Math.log10(raw)));
  const norm = raw / mag;
  const nice = norm < 1.5 ? 1 : norm < 3 ? 2 : norm < 7 ? 5 : 10;
  return nice * mag;
}

export interface Axis {
  min: number;
  max: number;
  ticks: number[];
}

export function niceAxis(values: readonly number[], target = 5, includeZero = false): Axis {
  const finite = values.filter((v) => Number.isFinite(v));
  if (finite.length === 0) return { min: 0, max: 1, ticks: [0, 1] };
  let lo = Math.min(...finite);
  let hi = Math.max(...finite);
  if (includeZero) {
    lo = Math.min(lo, 0);
    hi = Math.max(hi, 0);
  }
  if (lo === hi) {
    lo -= 1;
    hi += 1;
  }
  const step = niceStep(hi - lo, target);
  const min = Math.floor(lo / step) * step;
  const max = Math.ceil(hi / step) * step;
  const ticks: number[] = [];
  for (let v = min, guard = 0; v <= max + step / 2 && guard < 100; v += step, guard++) ticks.push(Math.round(v / step) * step);
  return { min, max, ticks };
}

export function scale(domainMin: number, domainMax: number, rangeMin: number, rangeMax: number): (v: number) => number {
  const span = domainMax - domainMin || 1;
  return (v) => rangeMin + ((v - domainMin) / span) * (rangeMax - rangeMin);
}

/** A polyline path that breaks at gaps (null or non-finite values). */
export function linePath(points: readonly ({ x: number; y: number } | null)[]): string {
  let d = "";
  let pen = false;
  for (const p of points) {
    if (!p || !Number.isFinite(p.x) || !Number.isFinite(p.y)) {
      pen = false;
      continue;
    }
    d += `${pen ? "L" : "M"}${p.x.toFixed(2)} ${p.y.toFixed(2)}`;
    pen = true;
  }
  return d;
}

export function arcPath(cx: number, cy: number, r: number, from: number, to: number): string {
  const sweep = to - from;
  if (sweep >= Math.PI * 2 - 1e-6) return `M${cx - r} ${cy}a${r} ${r} 0 1 0 ${2 * r} 0a${r} ${r} 0 1 0 ${-2 * r} 0Z`;
  const x1 = cx + r * Math.sin(from);
  const y1 = cy - r * Math.cos(from);
  const x2 = cx + r * Math.sin(to);
  const y2 = cy - r * Math.cos(to);
  return `M${cx} ${cy}L${x1.toFixed(2)} ${y1.toFixed(2)}A${r} ${r} 0 ${sweep > Math.PI ? 1 : 0} 1 ${x2.toFixed(2)} ${y2.toFixed(2)}Z`;
}

export function formatTick(v: number): string {
  if (Math.abs(v) >= 10000) return new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 }).format(v);
  return String(Math.round(v * 1e6) / 1e6);
}

/** CSV with quotes escaped and formula-looking cells neutralised. */
export function rowsToCsv(columns: readonly { key: string; label: string }[], rows: readonly Record<string, unknown>[]): string {
  const cell = (v: unknown): string => {
    let s = v === null || v === undefined ? "" : typeof v === "object" ? JSON.stringify(v) : String(v);
    if (/^[=+\-@\t\r]/.test(s) && typeof v !== "number") s = `'${s}`;
    return /[",\n\r]/.test(s) ? `"${s.replace(/"/g, '""')}"` : s;
  };
  return [columns.map((c) => cell(c.label)).join(","), ...rows.map((r) => columns.map((c) => cell(r[c.key])).join(","))].join("\r\n");
}
