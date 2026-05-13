"use client";

import { memo, useEffect, useRef, useState, type ReactNode } from "react";
import { Download } from "lucide-react";
import { DropdownMenu, DropdownMenuContent, DropdownMenuItem, DropdownMenuTrigger, toast } from "@/components/ui";
import { useT } from "@/lib/i18n/provider";
import { cn } from "@/lib/utils/cn";
import { downloadText } from "../../lib/clipboard";
import type { Align, TableCell } from "../../lib/markdown/ast";
import { classifyTable, columnWidth, toCsv, toMarkdownTable, type ColumnKind } from "../../lib/markdown/table-class";

interface Props {
  header: TableCell[];
  align: Align[];
  rows: TableCell[][];
  streaming: boolean;
  renderCell: (cell: TableCell) => ReactNode;
}

export const MdTable = memo(function MdTable({ header, align, rows, streaming, renderCell }: Props) {
  const t = useT();
  const previous = useRef<ColumnKind[] | undefined>(undefined);
  const [overflowing, setOverflowing] = useState(false);
  const scroller = useRef<HTMLDivElement>(null);

  const headerText = header.map((h) => h.text);
  const rowText = rows.map((r) => r.map((c) => c.text));
  const kinds = classifyTable(headerText, rowText, previous.current, streaming);
  previous.current = kinds;
  const contentColumns = kinds.filter((k) => k === "content").length;

  const kindsKey = kinds.join();
  useEffect(() => {
    const el = scroller.current;
    if (!el) return;
    const measure = () => setOverflowing(el.scrollWidth - el.clientWidth > 2);
    measure();
    const ro = typeof ResizeObserver === "undefined" ? null : new ResizeObserver(measure);
    ro?.observe(el);
    return () => ro?.disconnect();
  }, [rows.length, kindsKey]);

  const narrow = typeof window !== "undefined" && window.innerWidth < 480;
  const download = (format: "csv" | "md") => {
    try {
      if (format === "csv") downloadText(toCsv(headerText, rowText), "table.csv", "text/csv;charset=utf-8");
      else downloadText(toMarkdownTable(headerText, rowText), "table.md", "text/markdown;charset=utf-8");
    } catch {
      toast.error(t("chatRich.md.tableDownloadFailed"));
    }
  };

  return (
    <div className="rio-table-wrap" data-md-table>
      <div
        ref={scroller}
        className="rio-table-scroll"
        role="region"
        aria-label={t("chatRich.md.scrollableTable")}
        tabIndex={overflowing ? 0 : -1}
        title={overflowing ? t("chatRich.md.scrollableTableHint") : undefined}
      >
        <table className="rio-table">
          <colgroup>
            {kinds.map((k, i) => {
              const w = columnWidth(k, contentColumns, narrow);
              return <col key={i} style={{ minWidth: `${w.min}rem`, width: k === "content" || k === "normal" ? `${w.max}rem` : undefined }} />;
            })}
          </colgroup>
          <thead>
            <tr>
              {header.map((h, i) => (
                <th key={i} className={cn(`c-${kinds[i] ?? "normal"}`)} style={{ textAlign: align[i] ?? undefined }}>
                  {renderCell(h)}
                </th>
              ))}
            </tr>
          </thead>
          <tbody>
            {rows.map((r, ri) => (
              <tr key={ri}>
                {r.map((c, ci) => (
                  <td key={ci} className={cn(`c-${kinds[ci] ?? "normal"}`)} style={{ textAlign: align[ci] ?? undefined }}>
                    {renderCell(c)}
                  </td>
                ))}
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      <div className="absolute -top-2.5 right-2 opacity-0 transition-opacity group-hover/msg:opacity-100 hover:opacity-100 focus-within:opacity-100 [@media(hover:none)]:opacity-100" data-capture-hide>
        <DropdownMenu>
          <DropdownMenuTrigger asChild>
            <button type="button" aria-label={t("chatRich.md.tableDownload")} title={t("chatRich.md.tableDownload")} className="rounded-md border bg-background p-1 text-muted-foreground shadow-sm hover:text-foreground">
              <Download className="size-3.5" />
            </button>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="end">
            <DropdownMenuItem onSelect={() => download("csv")}>{t("chatRich.md.tableCsv")}</DropdownMenuItem>
            <DropdownMenuItem onSelect={() => download("md")}>{t("chatRich.md.tableMarkdown")}</DropdownMenuItem>
          </DropdownMenuContent>
        </DropdownMenu>
      </div>
    </div>
  );
});
