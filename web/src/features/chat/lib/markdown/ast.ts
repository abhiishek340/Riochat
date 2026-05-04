/** The Markdown syntax tree. */

export type Inline =
  | { type: "text"; value: string }
  | { type: "code"; value: string }
  | { type: "em"; children: Inline[] }
  | { type: "strong"; children: Inline[] }
  | { type: "del"; children: Inline[] }
  | { type: "link"; href: string; title: string; children: Inline[] }
  | { type: "image"; src: string; alt: string; title: string }
  | { type: "math"; tex: string }
  | { type: "br" }
  | { type: "footnoteRef"; id: string; index: number };

export type Align = "left" | "center" | "right" | null;

export interface TableCell {
  children: Inline[];
  text: string;
}

export interface ListItem {
  checked: boolean | null;
  children: Block[];
}

export type Block =
  | { type: "paragraph"; children: Inline[]; line: number; raw: string }
  | { type: "heading"; level: number; children: Inline[]; id: string; text: string; line: number; raw: string }
  | { type: "code"; lang: string; info: string; code: string; closed: boolean; line: number; raw: string }
  | { type: "math"; tex: string; closed: boolean; line: number; raw: string }
  | { type: "quote"; children: Block[]; line: number; raw: string }
  | { type: "list"; ordered: boolean; start: number; tight: boolean; items: ListItem[]; line: number; raw: string }
  | { type: "table"; header: TableCell[]; align: Align[]; rows: TableCell[][]; line: number; raw: string; complete: boolean }
  | { type: "hr"; line: number; raw: string }
  | { type: "footnotes"; items: { id: string; index: number; children: Block[] }[]; line: number; raw: string };

export function inlineText(nodes: readonly Inline[]): string {
  let out = "";
  for (const n of nodes) {
    switch (n.type) {
      case "text":
      case "code":
        out += n.value;
        break;
      case "em":
      case "strong":
      case "del":
      case "link":
        out += inlineText(n.children);
        break;
      case "image":
        out += n.alt;
        break;
      case "math":
        out += n.tex;
        break;
      case "br":
        out += "\n";
        break;
      case "footnoteRef":
        break;
    }
  }
  return out;
}
