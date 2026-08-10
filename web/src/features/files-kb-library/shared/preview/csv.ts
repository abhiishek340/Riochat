/** Minimal RFC 4180 reader for the sheet viewer. Tolerates ragged rows and a final line without a newline. */
export function parseCsv(text: string, maxRows = Number.POSITIVE_INFINITY): { rows: string[][]; truncated: boolean } {
  const rows: string[][] = [];
  let row: string[] = [];
  let cell = "";
  let quoted = false;
  const src = text.charCodeAt(0) === 0xfeff ? text.slice(1) : text;
  const endRow = (): boolean => {
    row.push(cell);
    cell = "";
    // A completely empty line carries no data.
    if (!(row.length === 1 && row[0] === "")) rows.push(row);
    row = [];
    return rows.length >= maxRows;
  };
  for (let i = 0; i < src.length; i += 1) {
    const ch = src[i] as string;
    if (quoted) {
      if (ch === '"') {
        if (src[i + 1] === '"') {
          cell += '"';
          i += 1;
        } else {
          quoted = false;
        }
      } else {
        cell += ch;
      }
      continue;
    }
    if (ch === '"' && cell === "") quoted = true;
    else if (ch === ",") {
      row.push(cell);
      cell = "";
    } else if (ch === "\n" || ch === "\r") {
      if (ch === "\r" && src[i + 1] === "\n") i += 1;
      if (endRow()) return { rows, truncated: src.slice(i + 1).trim() !== "" };
    } else cell += ch;
  }
  if (cell !== "" || row.length > 0) endRow();
  return { rows, truncated: false };
}
