/** Per-call tool prices are typed in USD and stored as nano-USD. */
import type { NativeToolPrice } from "../lib/wire";

const NANO = 1_000_000_000;

export function usdToNano(usd: number): number {
  return Math.round(usd * NANO);
}

export function nanoToUsd(nano: number): number {
  return nano / NANO;
}

/** The USD text of a stored price; zero shows as "0". */
export function nanoText(nano: number): string {
  return nano === 0 ? "0" : String(nanoToUsd(nano));
}

export type PriceParse = { ok: true; nano: number } | { ok: false };

/** A decimal USD price that is zero or more. */
export function parseUsdPrice(text: string): PriceParse {
  const t = text.trim();
  if (t === "") return { ok: true, nano: 0 };
  if (!/^\d*\.?\d+$|^\d+\.$/.test(t)) return { ok: false };
  const n = Number(t);
  return Number.isFinite(n) && n >= 0 ? { ok: true, nano: usdToNano(n) } : { ok: false };
}

export interface NativeSave {
  toolKey: string;
  priceNanousd: number;
  unit: "call";
  priceLabel: string;
  billable: boolean;
}

export function nativeBody(tool: Pick<NativeToolPrice, "toolKey">, nano: number): NativeSave {
  return { toolKey: tool.toolKey, priceNanousd: nano, unit: "call", priceLabel: "", billable: nano > 0 };
}

/** Draft text per tool key against what is stored; invalid typing keeps the stored value. */
export function nativeChanges(tools: readonly NativeToolPrice[], drafts: Readonly<Record<string, string>>): NativeSave[] {
  const out: NativeSave[] = [];
  for (const tool of tools) {
    const text = drafts[tool.toolKey];
    if (text === undefined) continue;
    const parsed = parseUsdPrice(text);
    const nano = parsed.ok ? parsed.nano : tool.priceNanousd;
    if (nano !== tool.priceNanousd) out.push(nativeBody(tool, nano));
  }
  return out;
}

/** Every tool with its effective price, ready to send. */
export function nativeFull(tools: readonly NativeToolPrice[], drafts: Readonly<Record<string, string>>): NativeSave[] {
  return tools.map((tool) => {
    const parsed = drafts[tool.toolKey] === undefined ? null : parseUsdPrice(drafts[tool.toolKey] as string);
    return nativeBody(tool, parsed?.ok ? parsed.nano : tool.priceNanousd);
  });
}
