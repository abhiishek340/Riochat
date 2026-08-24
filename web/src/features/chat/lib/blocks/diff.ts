/** Line diff for the diff viewer: LCS over lines, grouped into hunks with context. */

export type DiffKind = "same" | "add" | "del";

export interface DiffLine {
  kind: DiffKind;
  text: string;
  /** 1-based numbers in the old and new text, 0 when absent. */
  oldNo: number;
  newNo: number;
}

export const MAX_DIFF_LINES = 2000;

function splitLines(text: string): string[] {
  if (text === "") return [];
  const lines = text.replace(/\r\n?/g, "\n").split("\n");
  if (lines[lines.length - 1] === "") lines.pop();
  return lines;
}

export function diffLines(before: string, after: string): DiffLine[] {
  const a = splitLines(before).slice(0, MAX_DIFF_LINES);
  const b = splitLines(after).slice(0, MAX_DIFF_LINES);
  // Trim the common prefix and suffix so the table stays small.
  let start = 0;
  while (start < a.length && start < b.length && a[start] === b[start]) start++;
  let endA = a.length;
  let endB = b.length;
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--;
    endB--;
  }
  const n = endA - start;
  const m = endB - start;
  const table: Uint32Array[] = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
  for (let i = n - 1; i >= 0; i--) {
    for (let j = m - 1; j >= 0; j--) {
      table[i]![j] = a[start + i] === b[start + j] ? table[i + 1]![j + 1]! + 1 : Math.max(table[i + 1]![j]!, table[i]![j + 1]!);
    }
  }
  const out: DiffLine[] = [];
  let oldNo = 0;
  let newNo = 0;
  const push = (kind: DiffKind, text: string) => {
    if (kind !== "add") oldNo++;
    if (kind !== "del") newNo++;
    out.push({ kind, text, oldNo: kind === "add" ? 0 : oldNo, newNo: kind === "del" ? 0 : newNo });
  };
  for (let i = 0; i < start; i++) push("same", a[i]!);
  let i = 0;
  let j = 0;
  while (i < n && j < m) {
    if (a[start + i] === b[start + j]) {
      push("same", a[start + i]!);
      i++;
      j++;
    } else if (table[i + 1]![j]! >= table[i]![j + 1]!) {
      push("del", a[start + i]!);
      i++;
    } else {
      push("add", b[start + j]!);
      j++;
    }
  }
  while (i < n) push("del", a[start + i++]!);
  while (j < m) push("add", b[start + j++]!);
  for (let k = endA; k < a.length; k++) push("same", a[k]!);
  return out;
}

export type DiffRow = { type: "line"; line: DiffLine } | { type: "fold"; count: number; lines: DiffLine[] };

/** Folds runs of unchanged lines longer than `2 * context` unless `expanded`. */
export function foldDiff(lines: readonly DiffLine[], context: number, expanded: boolean): DiffRow[] {
  if (expanded) return lines.map((line) => ({ type: "line", line }));
  const keep = new Array<boolean>(lines.length).fill(false);
  lines.forEach((l, i) => {
    if (l.kind === "same") return;
    for (let k = Math.max(0, i - context); k <= Math.min(lines.length - 1, i + context); k++) keep[k] = true;
  });
  const rows: DiffRow[] = [];
  let i = 0;
  while (i < lines.length) {
    if (keep[i]) {
      rows.push({ type: "line", line: lines[i]! });
      i++;
      continue;
    }
    let j = i;
    while (j < lines.length && !keep[j]) j++;
    // A run with nothing changed at all is shown in full: there is nothing to focus on.
    if (lines.every((l) => l.kind === "same")) return lines.map((line) => ({ type: "line", line }));
    rows.push({ type: "fold", count: j - i, lines: lines.slice(i, j) });
    i = j;
  }
  return rows;
}

/** Indexes (in `rows`) where a change starts, for previous/next navigation. */
export function changeStarts(rows: readonly DiffRow[]): number[] {
  const out: number[] = [];
  let prevChanged = false;
  rows.forEach((r, i) => {
    const changed = r.type === "line" && r.line.kind !== "same";
    if (changed && !prevChanged) out.push(i);
    prevChanged = changed;
  });
  return out;
}
